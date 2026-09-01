package bundle

import (
	"archive/tar"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type Report struct {
	Manifest  Manifest
	FileCount int
	TotalSize int64
}

type payloadSink func(File, io.Reader) error

func Inspect(path string) (*Report, error) {
	archiveFile, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bundle %q: %w", path, err)
	}
	defer archiveFile.Close()

	info, err := archiveFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bundle %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("bundle %q must be a non-empty regular file", path)
	}

	decoder, err := zstd.NewReader(
		archiveFile,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(1<<30),
		zstd.WithDecoderMaxWindow(128<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("open zstd stream in bundle %q: %w", path, err)
	}
	defer decoder.Close()

	report, err := readArchive(tar.NewReader(decoder), nil)
	if err != nil {
		return nil, fmt.Errorf("inspect bundle %q: %w", path, err)
	}

	// tar 结束标记之后不允许继续携带未声明数据或另一个拼接归档。
	var trailing [1]byte
	count, trailingErr := decoder.Read(trailing[:])
	if count != 0 || trailingErr == nil {
		return nil, fmt.Errorf("inspect bundle %q: archive contains trailing data", path)
	}
	if trailingErr != io.EOF {
		return nil, fmt.Errorf("inspect bundle %q: read archive trailer: %w", path, trailingErr)
	}
	return report, nil
}

// Extract 将已校验的 Bundle 载荷解包到一个新的临时目录或空目录中。
func Extract(path, destination string) (*Report, error) {
	if !filepath.IsAbs(destination) {
		return nil, fmt.Errorf("bundle extraction destination must be an absolute path")
	}
	if err := prepareExtractionDirectory(destination); err != nil {
		return nil, err
	}

	archiveFile, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bundle %q: %w", path, err)
	}
	defer archiveFile.Close()
	info, err := archiveFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bundle %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("bundle %q must be a non-empty regular file", path)
	}
	decoder, err := zstd.NewReader(
		archiveFile,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(1<<30),
		zstd.WithDecoderMaxWindow(128<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("open zstd stream in bundle %q: %w", path, err)
	}
	defer decoder.Close()

	sink := func(file File, reader io.Reader) error {
		outputPath := filepath.Join(destination, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
			return fmt.Errorf("create extraction directory for %q: %w", file.Path, err)
		}
		output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create extracted payload %q: %w", file.Path, err)
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		if copyErr != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("write extracted payload %q: %w", file.Path, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("close extracted payload %q: %w", file.Path, closeErr)
		}
		return nil
	}

	report, err := readArchive(tar.NewReader(decoder), sink)
	if err != nil {
		return nil, fmt.Errorf("extract bundle %q: %w", path, err)
	}
	var trailing [1]byte
	count, trailingErr := decoder.Read(trailing[:])
	if count != 0 || trailingErr == nil {
		return nil, fmt.Errorf("extract bundle %q: archive contains trailing data", path)
	}
	if trailingErr != io.EOF {
		return nil, fmt.Errorf("extract bundle %q: read archive trailer: %w", path, trailingErr)
	}
	return report, nil
}

func prepareExtractionDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create extraction directory %q: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat extraction directory %q: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("extraction destination %q must be a directory and not a symbolic link", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read extraction directory %q: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("extraction destination %q must be empty", path)
	}
	return nil
}

func readArchive(reader *tar.Reader, sink payloadSink) (*Report, error) {
	header, err := reader.Next()
	if err == io.EOF {
		return nil, fmt.Errorf("archive does not contain manifest.yaml")
	}
	if err != nil {
		return nil, err
	}
	if header.Name != ManifestPath || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
		return nil, fmt.Errorf("manifest.yaml must be the first regular archive entry")
	}
	if header.Size > maxManifestSize {
		return nil, fmt.Errorf("manifest.yaml exceeds %d bytes", maxManifestSize)
	}
	manifestData, err := io.ReadAll(io.LimitReader(reader, maxManifestSize+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest.yaml: %w", err)
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return nil, err
	}

	declared := make(map[string]File, len(manifest.Spec.Files))
	for _, file := range manifest.Spec.Files {
		declared[file.Path] = file
	}
	seenPayloads := make(map[string]struct{}, len(declared))
	seenEntries := map[string]struct{}{ManifestPath: {}}
	entryCount := 1
	var totalSize int64

	for {
		header, err = reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return nil, fmt.Errorf("archive must not contain more than %d entries", maxArchiveEntries)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || !fs.ValidPath(name) || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("archive contains unsafe path %q", header.Name)
		}
		if _, exists := seenEntries[name]; exists {
			return nil, fmt.Errorf("archive contains duplicate path %q", name)
		}
		seenEntries[name] = struct{}{}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return nil, fmt.Errorf("archive path %q uses unsupported entry type", name)
		}

		file, exists := declared[name]
		if !exists {
			return nil, fmt.Errorf("payload %q is not declared in manifest.yaml", name)
		}
		if header.Size != file.Size {
			return nil, fmt.Errorf("payload %q size is %d, want %d", name, header.Size, file.Size)
		}

		hash := sha256.New()
		limitedReader := io.LimitReader(reader, file.Size)
		countedReader := &countingReader{reader: io.TeeReader(limitedReader, hash)}
		size := int64(0)
		if sink == nil {
			size, err = io.Copy(io.Discard, countedReader)
		} else {
			if err := sink(file, countedReader); err != nil {
				return nil, err
			}
			size = countedReader.count
		}
		if err != nil {
			return nil, fmt.Errorf("read payload %q: %w", name, err)
		}
		if size != file.Size {
			return nil, fmt.Errorf("payload %q size is %d, want %d", name, size, file.Size)
		}
		if actual := fmt.Sprintf("%x", hash.Sum(nil)); actual != file.SHA256 {
			return nil, fmt.Errorf("payload %q SHA-256 does not match", name)
		}
		seenPayloads[name] = struct{}{}
		totalSize += size
	}
	for _, file := range manifest.Spec.Files {
		if _, exists := seenPayloads[file.Path]; !exists {
			return nil, fmt.Errorf("payload %q is missing", file.Path)
		}
	}

	return &Report{
		Manifest:  *manifest,
		FileCount: len(seenPayloads),
		TotalSize: totalSize,
	}, nil
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.count += int64(count)
	return count, err
}
