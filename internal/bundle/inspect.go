package bundle

import (
	"archive/tar"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type Report struct {
	Manifest  Manifest
	FileCount int
	TotalSize int64
}

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

	report, err := readArchive(tar.NewReader(decoder))
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

func readArchive(reader *tar.Reader) (*Report, error) {
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
		size, err := io.Copy(hash, reader)
		if err != nil {
			return nil, fmt.Errorf("hash payload %q: %w", name, err)
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
