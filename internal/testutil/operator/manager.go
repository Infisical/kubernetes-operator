package operator

import (
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

type Manager struct {
	client          client.Client
	inClusterAPIURL string
}

func (m *Manager) Client() client.Client   { return m.client }
func (m *Manager) InClusterAPIURL() string { return m.inClusterAPIURL }

func (m *Manager) Stop() {
	cmd := exec.Command("helm", "uninstall", helmReleaseName, "--namespace", helmNamespace, "--wait")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "helm uninstall (cleanup): %s: %v\n", out, err)
	}
}

type InstallOpts struct {
	HostAPIURL               string
	APIContainerID           string // Container running the Infisical API. It is attached to the Kind network so pods can reach it directly.
	APIContainerPort         string // Port the API listens on inside its container.
	DefaultConnection        bool   // If true, install a default InfisicalConnection in the operator namespace
	DefaultConnectionAddress string // Address for the default InfisicalConnection. The host is replaced with the in-cluster API address; the path is preserved. If empty, defaults to the in-cluster API URL.
}

func Install(opts InstallOpts) (*Manager, error) {
	root := projectRoot()
	chartPath := filepath.Join(root, "helm-charts", "secrets-operator")

	// Remove any pre-existing CRDs so Helm can manage them cleanly.
	_ = runDir(root, "make", "uninstall", "ignore-not-found=true")

	// Attach the API container to the Kind network so pods reach it by container IP.
	// Going through the host's port mapping via the network gateway only works when
	// Docker runs natively on Linux, not with Docker Desktop, Colima or OrbStack.
	apiIP, err := attachToNetwork(kindNetwork, opts.APIContainerID)
	if err != nil {
		return nil, fmt.Errorf("attach API container to kind network: %w", err)
	}
	apiHost := net.JoinHostPort(apiIP, opts.APIContainerPort)
	inClusterURL := "http://" + apiHost

	if err := waitForAPI(opts.HostAPIURL, 60*time.Second); err != nil {
		return nil, fmt.Errorf("API not ready: %w", err)
	}

	valuesFile, err := os.CreateTemp("", "e2e-helm-values-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temp values file: %w", err)
	}
	defer os.Remove(valuesFile.Name())

	connAddress := inClusterURL
	if opts.DefaultConnectionAddress != "" {
		connURL, err := url.Parse(opts.DefaultConnectionAddress)
		if err != nil {
			return nil, fmt.Errorf("parse default connection address: %w", err)
		}
		connURL.Host = apiHost
		connAddress = connURL.String()
	}

	valuesContent := fmt.Sprintf(`hostAPI: %q
controllerManager:
  manager:
    image:
      repository: infisical/kubernetes-operator
      tag: e2e-test
    args:
    - --metrics-bind-address=:8443
    - --health-probe-bind-address=:8081
defaultInfisicalConnection:
  enabled: %t
  address: %q
`, inClusterURL, opts.DefaultConnection, connAddress)

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

	return &Manager{
		client:          k8sClient,
		inClusterAPIURL: inClusterURL,
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

// attachToNetwork connects the container to the Docker network (if not already
// connected) and returns its IPv4 address on that network.
func attachToNetwork(networkName, containerID string) (string, error) {
	format := fmt.Sprintf("{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}", networkName)
	ip, err := cmdOutput("docker", "inspect", containerID, "--format", format)
	if err != nil {
		return "", err
	}
	if ip == "" {
		if _, err := cmdOutput("docker", "network", "connect", networkName, containerID); err != nil {
			return "", err
		}
		if ip, err = cmdOutput("docker", "inspect", containerID, "--format", format); err != nil {
			return "", err
		}
	}
	if net.ParseIP(ip).To4() == nil {
		return "", fmt.Errorf("container %s has no IPv4 address on network %q", containerID, networkName)
	}
	return ip, nil
}
