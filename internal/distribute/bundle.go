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
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
)

// FileTransport 是远程载荷传输需要的最小接口，便于测试和后续替换传输实现。
type FileTransport interface {
	UploadFile(context.Context, string, string) error
	VerifySHA256(context.Context, string, string) error
}

// Report 描述一次成功分发的 Bundle。
type Report struct {
	Manifest   bundle.Manifest
	RemoteRoot string
	Files      []bundle.File
}

// Push 校验本地 Bundle 并将全部载荷上传到远程目录。
// 每个文件上传完成后都由远程节点再次校验 SHA-256。
func Push(ctx context.Context, transport FileTransport, bundlePath, remoteRoot string) (*Report, error) {
	if transport == nil {
		return nil, fmt.Errorf("bundle file transport is required")
	}
	if !filepath.IsAbs(bundlePath) {
		return nil, fmt.Errorf("bundle path must be absolute")
	}
	if !path.IsAbs(remoteRoot) || path.Clean(remoteRoot) == "/" {
		return nil, fmt.Errorf("remote bundle directory must be an absolute non-root path")
	}

	extractionDirectory, err := os.MkdirTemp("", "kubelift-bundle-")
	if err != nil {
		return nil, fmt.Errorf("create temporary bundle directory: %w", err)
	}
	defer os.RemoveAll(extractionDirectory)

	inspection, err := bundle.Extract(bundlePath, extractionDirectory)
	if err != nil {
		return nil, err
	}
	for _, file := range inspection.Manifest.Spec.Files {
		localPath := filepath.Join(extractionDirectory, filepath.FromSlash(file.Path))
		remotePath, err := remotePayloadPath(remoteRoot, file.Path)
		if err != nil {
			return nil, err
		}
		if err := transport.UploadFile(ctx, localPath, remotePath); err != nil {
			return nil, fmt.Errorf("upload bundle payload %q: %w", file.Path, err)
		}
		if err := transport.VerifySHA256(ctx, remotePath, file.SHA256); err != nil {
			return nil, fmt.Errorf("verify bundle payload %q: %w", file.Path, err)
		}
	}

	return &Report{
		Manifest:   inspection.Manifest,
		RemoteRoot: path.Clean(remoteRoot),
		Files:      append([]bundle.File(nil), inspection.Manifest.Spec.Files...),
	}, nil
}

func remotePayloadPath(remoteRoot, payloadPath string) (string, error) {
	if !fs.ValidPath(payloadPath) || strings.Contains(payloadPath, `\`) {
		return "", fmt.Errorf("bundle payload path %q is unsafe", payloadPath)
	}
	joined := path.Join(remoteRoot, payloadPath)
	rootPrefix := strings.TrimSuffix(path.Clean(remoteRoot), "/") + "/"
	if !strings.HasPrefix(joined, rootPrefix) {
		return "", fmt.Errorf("bundle payload path %q escapes remote directory", payloadPath)
	}
	return joined, nil
}
