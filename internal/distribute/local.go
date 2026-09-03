/*
Copyright © 2026 QX-hao

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package distribute

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalTransport 将 Bundle 载荷安全地写入 Master0 本地 staging 目录。
type LocalTransport struct{}

// UploadFile 先写入同目录临时文件，成功后再原子替换目标文件。
func (LocalTransport) UploadFile(ctx context.Context, sourcePath, destinationPath string) error {
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(destinationPath) {
		return fmt.Errorf("local copy paths must be absolute")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open local payload %q: %w", sourcePath, err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat local payload %q: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local payload %q must be a regular file", sourcePath)
	}

	destinationDirectory := filepath.Dir(destinationPath)
	if err := os.MkdirAll(destinationDirectory, 0o700); err != nil {
		return fmt.Errorf("create local staging directory %q: %w", destinationDirectory, err)
	}
	temporary, err := os.CreateTemp(destinationDirectory, ".kubelift-partial-")
	if err != nil {
		return fmt.Errorf("create local staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	copyErr := copyWithContext(ctx, temporary, source)
	chmodErr := temporary.Chmod(0o600)
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("copy local payload %q: %w", sourcePath, copyErr)
	}
	if chmodErr != nil {
		return fmt.Errorf("set local payload permissions: %w", chmodErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close local staging file: %w", closeErr)
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		return fmt.Errorf("replace local staged payload %q: %w", destinationPath, err)
	}
	return nil
}

// VerifySHA256 复核 Master0 staging 文件，和远程 SSH 分发使用相同校验语义。
func (LocalTransport) VerifySHA256(ctx context.Context, path, expected string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("local verification path must be absolute")
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("expected SHA-256 must contain 64 hexadecimal characters")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open staged payload %q: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if err := copyWithContext(ctx, hash, file); err != nil {
		return fmt.Errorf("hash staged payload %q: %w", path, err)
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("staged payload %q SHA-256 is %q, want %q", path, actual, expected)
	}
	return nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, err := destination.Write(buffer[:count])
			if err != nil {
				return err
			}
			if written != count {
				return io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
