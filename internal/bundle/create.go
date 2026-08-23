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
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func Create(sourceDirectory, outputPath string) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("bundle output path is required")
	}
	sourceAbsolute, err := filepath.Abs(sourceDirectory)
	if err != nil {
		return fmt.Errorf("resolve bundle source path: %w", err)
	}
	outputAbsolute, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve bundle output path: %w", err)
	}
	relativeOutput, err := filepath.Rel(sourceAbsolute, outputAbsolute)
	if err != nil {
		return fmt.Errorf("compare bundle source and output paths: %w", err)
	}
	if relativeOutput == "." || (relativeOutput != ".." && !strings.HasPrefix(relativeOutput, ".."+string(filepath.Separator))) {
		return fmt.Errorf("bundle output must be outside the source directory")
	}

	manifestData, manifest, err := verifySourceDirectory(sourceDirectory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create bundle output directory: %w", err)
	}

	// 输出使用 O_EXCL，避免自动化流程误覆盖已经分发的离线包。
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("bundle output %q already exists", outputPath)
		}
		return fmt.Errorf("create bundle output %q: %w", outputPath, err)
	}
	if err := writeArchive(output, sourceDirectory, manifestData, manifest); err != nil {
		_ = output.Close()
		_ = os.Remove(outputPath)
		return fmt.Errorf("create bundle %q: %w", outputPath, err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("close bundle %q: %w", outputPath, err)
	}
	// 源文件可能在校验与打包之间变化，最终产物必须重新完整校验一次。
	if _, err := Inspect(outputPath); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("verify created bundle %q: %w", outputPath, err)
	}
	return nil
}

func verifySourceDirectory(sourceDirectory string) ([]byte, *Manifest, error) {
	info, err := os.Stat(sourceDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("stat bundle source %q: %w", sourceDirectory, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("bundle source %q must be a directory", sourceDirectory)
	}

	manifestPath := filepath.Join(sourceDirectory, ManifestPath)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat source manifest %q: %w", manifestPath, err)
	}
	if !manifestInfo.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("source manifest %q must be a regular file", manifestPath)
	}
	if manifestInfo.Size() > maxManifestSize {
		return nil, nil, fmt.Errorf("source manifest %q exceeds %d bytes", manifestPath, maxManifestSize)
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open source manifest %q: %w", manifestPath, err)
	}
	manifestData, readErr := io.ReadAll(io.LimitReader(manifestFile, maxManifestSize+1))
	closeErr := manifestFile.Close()
	if readErr != nil {
		return nil, nil, fmt.Errorf("read source manifest %q: %w", manifestPath, readErr)
	}
	if closeErr != nil {
		return nil, nil, fmt.Errorf("close source manifest %q: %w", manifestPath, closeErr)
	}
	if len(manifestData) > maxManifestSize {
		return nil, nil, fmt.Errorf("source manifest %q exceeds %d bytes", manifestPath, maxManifestSize)
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return nil, nil, err
	}

	declared := make(map[string]File, len(manifest.Spec.Files))
	for _, file := range manifest.Spec.Files {
		declared[file.Path] = file
		if err := verifySourceFile(sourceDirectory, file); err != nil {
			return nil, nil, err
		}
	}

	err = filepath.WalkDir(sourceDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceDirectory, path)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source path %q must not be a symbolic link", relative)
		}
		if relative == ManifestPath {
			return nil
		}
		if _, exists := declared[relative]; !exists {
			return fmt.Errorf("source payload %q is not declared in manifest.yaml", relative)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect bundle source %q: %w", sourceDirectory, err)
	}
	return manifestData, manifest, nil
}

func verifySourceFile(sourceDirectory string, file File) error {
	path := filepath.Join(sourceDirectory, filepath.FromSlash(file.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat source payload %q: %w", file.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source payload %q must be a regular file", file.Path)
	}
	if info.Size() != file.Size {
		return fmt.Errorf("source payload %q size is %d, want %d", file.Path, info.Size(), file.Size)
	}

	payload, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source payload %q: %w", file.Path, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, payload)
	closeErr := payload.Close()
	if copyErr != nil {
		return fmt.Errorf("hash source payload %q: %w", file.Path, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close source payload %q: %w", file.Path, closeErr)
	}
	if actual := fmt.Sprintf("%x", hash.Sum(nil)); actual != file.SHA256 {
		return fmt.Errorf("source payload %q SHA-256 does not match manifest.yaml", file.Path)
	}
	return nil
}

func writeArchive(output io.Writer, sourceDirectory string, manifestData []byte, manifest *Manifest) error {
	encoder, err := zstd.NewWriter(output, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	archive := tar.NewWriter(encoder)
	if err := writeTarFile(archive, ManifestPath, manifestData); err != nil {
		_ = archive.Close()
		encoder.Close()
		return err
	}

	files := append([]File(nil), manifest.Spec.Files...)
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	for _, file := range files {
		path := filepath.Join(sourceDirectory, filepath.FromSlash(file.Path))
		if err := writeTarPath(archive, file.Path, path, file.Size); err != nil {
			_ = archive.Close()
			encoder.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		encoder.Close()
		return err
	}
	return encoder.Close()
}

func writeTarFile(archive *tar.Writer, name string, contents []byte) error {
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     int64(len(contents)),
		Typeflag: tar.TypeReg,
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	_, err := archive.Write(contents)
	return err
}

func writeTarPath(archive *tar.Writer, name, path string, size int64) error {
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     size,
		Typeflag: tar.TypeReg,
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	payload, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(archive, payload)
	closeErr := payload.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
