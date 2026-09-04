package workflow

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/QX-hao/kubelift/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	createStateAPIVersion = "kubelift.io/v1alpha1"
	createStateKind       = "CreateState"
)

type CreatePhase string

const (
	PhaseStaged             CreatePhase = "staged"
	PhasePrepared           CreatePhase = "prepared"
	PhaseImagesImported     CreatePhase = "images-imported"
	PhaseKubeadmStarting    CreatePhase = "kubeadm-starting"
	PhaseKubeadmInitialized CreatePhase = "kubeadm-initialized"
	PhaseCiliumReady        CreatePhase = "cilium-ready"
	PhaseRegistryReady      CreatePhase = "registry-ready"
	PhaseComplete           CreatePhase = "complete"
)

var createPhaseOrder = map[CreatePhase]int{
	PhaseStaged: 1, PhasePrepared: 2, PhaseImagesImported: 3,
	PhaseKubeadmStarting: 4, PhaseKubeadmInitialized: 5,
	PhaseCiliumReady: 6, PhaseRegistryReady: 7, PhaseComplete: 8,
}

type createState struct {
	APIVersion          string      `yaml:"apiVersion"`
	Kind                string      `yaml:"kind"`
	Cluster             string      `yaml:"cluster"`
	KubernetesVersion   string      `yaml:"kubernetesVersion"`
	ConfigurationSHA256 string      `yaml:"configurationSHA256"`
	BundleSHA256        string      `yaml:"bundleSHA256"`
	Phase               CreatePhase `yaml:"phase"`
}

func newCreateState(configuration config.Config, configHash, bundleHash string) createState {
	return createState{
		APIVersion: createStateAPIVersion, Kind: createStateKind,
		Cluster:             configuration.Metadata.Name,
		KubernetesVersion:   configuration.Spec.Kubernetes.Version,
		ConfigurationSHA256: configHash, BundleSHA256: bundleHash,
	}
}

func (s createState) reached(phase CreatePhase) bool {
	return createPhaseOrder[s.Phase] >= createPhaseOrder[phase]
}

func loadCreateState(path string) (createState, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return createState{}, err
	}
	var state createState
	if err := yaml.Unmarshal(contents, &state); err != nil {
		return createState{}, fmt.Errorf("decode create state: %w", err)
	}
	if state.APIVersion != createStateAPIVersion || state.Kind != createStateKind {
		return createState{}, fmt.Errorf("unsupported create state %q %q", state.APIVersion, state.Kind)
	}
	if _, exists := createPhaseOrder[state.Phase]; !exists {
		return createState{}, fmt.Errorf("unsupported create phase %q", state.Phase)
	}
	return state, nil
}

func saveCreateState(path string, state createState, phase CreatePhase) error {
	state.Phase = phase
	contents, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode create state: %w", err)
	}
	if err := writePrivateFile(path, contents); err != nil {
		return fmt.Errorf("save create state: %w", err)
	}
	return nil
}

func configurationHash(configuration config.Config) (string, error) {
	contents, err := yaml.Marshal(configuration)
	if err != nil {
		return "", fmt.Errorf("encode cluster configuration for state: %w", err)
	}
	value := sha256.Sum256(contents)
	return fmt.Sprintf("%x", value), nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open offline bundle for state: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash offline bundle for state: %w", err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func createStatePath(root, cluster string) string {
	return filepath.Join(root, cluster+".yaml")
}
