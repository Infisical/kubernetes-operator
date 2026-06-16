package operator

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1alpha1 "github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
)

const (
	helmReleaseName = "infisical-e2e"
	helmNamespace   = "infisical-operator-system"
	testImage       = "infisical/kubernetes-operator:e2e-test"
	kindNetwork     = "kind"
)

type KubernetesClusterInfo struct {
	Host   string
	CACert string
}

type Manager struct {
	client          client.Client
	inClusterAPIURL string
	clusterInfo     *KubernetesClusterInfo
}

func (m *Manager) Client() client.Client               { return m.client }
func (m *Manager) InClusterAPIURL() string             { return m.inClusterAPIURL }
func (m *Manager) ClusterInfo() *KubernetesClusterInfo { return m.clusterInfo }

func (m *Manager) Stop() {
	cmd := exec.Command("helm", "uninstall", helmReleaseName, "--namespace", helmNamespace, "--wait")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "helm uninstall (cleanup): %s: %v\n", out, err)
	}
}

type InstallOpts struct {
	HostAPIURL      string
	ScopedNamespace string
}

func Install(opts InstallOpts) (*Manager, error) {
	root := projectRoot()
	chartPath := filepath.Join(root, "helm-charts", "secrets-operator")

	// Remove any pre-existing CRDs so Helm can manage them cleanly.
	_ = runDir(root, "make", "uninstall", "ignore-not-found=true")

	// The Kind network gateway is the host IP from within Kind pods.
	// This lets the operator pod reach the testcontainer API via the host's port mapping.
	gateway, err := kindIPv4Gateway(kindNetwork)
	if err != nil {
		return nil, fmt.Errorf("get kind network gateway: %w", err)
	}

	hostURL, err := url.Parse(opts.HostAPIURL)
	if err != nil {
		return nil, fmt.Errorf("parse host URL: %w", err)
	}
	inClusterURL := fmt.Sprintf("http://%s:%s", gateway, hostURL.Port())

	if err := waitForAPI(opts.HostAPIURL, 60*time.Second); err != nil {
		return nil, fmt.Errorf("API not ready: %w", err)
	}

	valuesFile, err := os.CreateTemp("", "e2e-helm-values-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temp values file: %w", err)
	}
	defer os.Remove(valuesFile.Name())

	valuesContent := fmt.Sprintf(`hostAPI: %q
controllerManager:
  manager:
    image:
      repository: infisical/kubernetes-operator
      tag: e2e-test
    args:
    - --metrics-bind-address=:8443
    - --health-probe-bind-address=:8081
`, inClusterURL)

	if opts.ScopedNamespace != "" {
		valuesContent += fmt.Sprintf(`scopedNamespaces:
  - %q
scopedRBAC: true
`, opts.ScopedNamespace)
	}

	if _, err := valuesFile.WriteString(valuesContent); err != nil {
		return nil, fmt.Errorf("write values file: %w", err)
	}
	valuesFile.Close()

	if err := run("helm", "upgrade", "--install", helmReleaseName, chartPath,
		"--namespace", helmNamespace,
		"--create-namespace",
		"--values", valuesFile.Name(),
		"--wait",
		"--timeout", "120s",
	); err != nil {
		return nil, fmt.Errorf("helm install: %w", err)
	}

	if err := waitForCRDs(30*time.Second, []string{
		"infisicalconnections.secrets.infisical.com",
		"infisicalauths.secrets.infisical.com",
		"infisicalstaticsecrets.secrets.infisical.com",
	}); err != nil {
		return nil, fmt.Errorf("wait for CRDs: %w", err)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(secretsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(secretsv1beta1.AddToScheme(scheme))

	k8sClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create k8s client: %w", err)
	}

	clusterInfo, err := buildClusterInfo(kindNetwork)
	if err != nil {
		return nil, fmt.Errorf("get cluster info: %w", err)
	}

	return &Manager{
		client:          k8sClient,
		inClusterAPIURL: inClusterURL,
		clusterInfo:     clusterInfo,
	}, nil
}

func waitForAPI(baseURL string, timeout time.Duration) error {
	endpoint := baseURL + "/api/status"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)

	for {
		resp, err := httpClient.Get(endpoint)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s (last error: %v)", endpoint, err)
		}
		time.Sleep(time.Second)
	}
}

func projectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func waitForCRDs(timeout time.Duration, crds []string) error {
	cfg := ctrl.GetConfigOrDie()
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create discovery client: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for _, crd := range crds {
		for {
			resources, err := disc.ServerResourcesForGroupVersion("secrets.infisical.com/v1beta1")
			if err == nil {
				found := false
				for _, r := range resources.APIResources {
					if r.Name == strings.SplitN(crd, ".", 2)[0] {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for CRD %q to be discoverable", crd)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func kindIPv4Gateway(networkName string) (string, error) {
	out, err := cmdOutput("docker", "network", "inspect", networkName, "--format", "{{json .IPAM.Config}}")
	if err != nil {
		return "", err
	}

	type ipamConfig struct {
		Gateway string `json:"Gateway"`
	}

	var cfgs []ipamConfig
	if err := json.Unmarshal([]byte(out), &cfgs); err != nil {
		return "", fmt.Errorf("parse network IPAM config: %w", err)
	}

	for _, cfg := range cfgs {
		if cfg.Gateway == "" {
			continue
		}
		ip := net.ParseIP(cfg.Gateway)
		if ip == nil || ip.To4() == nil {
			continue
		}
		return cfg.Gateway, nil
	}

	return "", fmt.Errorf("no IPv4 gateway found in docker network %q IPAM config", networkName)
}

func buildClusterInfo(networkName string) (*KubernetesClusterInfo, error) {
	clusterName := os.Getenv("KIND_CLUSTER")
	if clusterName == "" {
		clusterName = "infisical-operator-test-e2e"
	}

	controlPlaneIP, err := cmdOutput(
		"docker", "inspect", clusterName+"-control-plane",
		"--format", fmt.Sprintf("{{(index .NetworkSettings.Networks %q).IPAddress}}", networkName),
	)
	if err != nil {
		return nil, fmt.Errorf("get control plane IP: %w", err)
	}

	caCert, err := cmdOutput("docker", "exec", clusterName+"-control-plane", "cat", "/etc/kubernetes/pki/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("get CA cert: %w", err)
	}

	return &KubernetesClusterInfo{
		Host:   fmt.Sprintf("https://%s:6443", controlPlaneIP),
		CACert: caCert,
	}, nil
}
