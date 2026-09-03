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
package kubeadm

import (
	"bytes"
	"fmt"
	"net"
	"strings"

	"github.com/QX-hao/kubelift/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	kubeadmAPIVersion = "kubeadm.k8s.io/v1beta3"
	criSocket         = "unix:///run/containerd/containerd.sock"
)

type initConfiguration struct {
	APIVersion       string                  `yaml:"apiVersion"`
	Kind             string                  `yaml:"kind"`
	NodeRegistration nodeRegistrationOptions `yaml:"nodeRegistration"`
	LocalAPIEndpoint apiEndpoint             `yaml:"localAPIEndpoint"`
	SkipPhases       []string                `yaml:"skipPhases"`
}

type nodeRegistrationOptions struct {
	CRISocket       string `yaml:"criSocket"`
	ImagePullPolicy string `yaml:"imagePullPolicy"`
}

type apiEndpoint struct {
	AdvertiseAddress string `yaml:"advertiseAddress"`
	BindPort         int    `yaml:"bindPort"`
}

type clusterConfiguration struct {
	APIVersion           string     `yaml:"apiVersion"`
	Kind                 string     `yaml:"kind"`
	ClusterName          string     `yaml:"clusterName"`
	KubernetesVersion    string     `yaml:"kubernetesVersion"`
	ControlPlaneEndpoint string     `yaml:"controlPlaneEndpoint,omitempty"`
	Networking           networking `yaml:"networking"`
	APIServer            apiServer  `yaml:"apiServer"`
}

type networking struct {
	PodSubnet     string `yaml:"podSubnet"`
	ServiceSubnet string `yaml:"serviceSubnet"`
	DNSDomain     string `yaml:"dnsDomain"`
}

type apiServer struct {
	CertSANs []string `yaml:"certSANs"`
}

type kubeletConfiguration struct {
	APIVersion   string `yaml:"apiVersion"`
	Kind         string `yaml:"kind"`
	CgroupDriver string `yaml:"cgroupDriver"`
}

// GenerateInitConfig 生成 Master0 使用的 kubeadm 多文档配置。
// 当前只支持 Kubernetes v1.28，后续版本需要按 kubeadm 配置 API 单独适配。
func GenerateInitConfig(configuration config.Config) ([]byte, error) {
	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if !strings.HasPrefix(configuration.Spec.Kubernetes.Version, "v1.28.") {
		return nil, fmt.Errorf("kubeadm configuration generation currently supports Kubernetes v1.28.x only")
	}

	certificateSANs := []string{configuration.Spec.ControlPlane.AdvertiseAddress}
	if endpoint := configuration.Spec.ControlPlane.Endpoint; endpoint != "" {
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse control-plane endpoint: %w", err)
		}
		if host != configuration.Spec.ControlPlane.AdvertiseAddress {
			certificateSANs = append(certificateSANs, host)
		}
	}

	documents := []any{
		initConfiguration{
			APIVersion: kubeadmAPIVersion,
			Kind:       "InitConfiguration",
			NodeRegistration: nodeRegistrationOptions{
				CRISocket:       criSocket,
				ImagePullPolicy: "Never",
			},
			LocalAPIEndpoint: apiEndpoint{
				AdvertiseAddress: configuration.Spec.ControlPlane.AdvertiseAddress,
				BindPort:         6443,
			},
			SkipPhases: []string{"addon/kube-proxy"},
		},
		clusterConfiguration{
			APIVersion:           kubeadmAPIVersion,
			Kind:                 "ClusterConfiguration",
			ClusterName:          configuration.Metadata.Name,
			KubernetesVersion:    configuration.Spec.Kubernetes.Version,
			ControlPlaneEndpoint: configuration.Spec.ControlPlane.Endpoint,
			Networking: networking{
				PodSubnet:     configuration.Spec.Network.PodCIDR,
				ServiceSubnet: configuration.Spec.Network.ServiceCIDR,
				DNSDomain:     "cluster.local",
			},
			APIServer: apiServer{CertSANs: certificateSANs},
		},
		kubeletConfiguration{
			APIVersion:   "kubelet.config.k8s.io/v1beta1",
			Kind:         "KubeletConfiguration",
			CgroupDriver: "systemd",
		},
	}

	var output bytes.Buffer
	for index, document := range documents {
		if index > 0 {
			output.WriteString("---\n")
		}
		contents, err := yaml.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("encode kubeadm configuration: %w", err)
		}
		output.Write(contents)
	}
	return output.Bytes(), nil
}
