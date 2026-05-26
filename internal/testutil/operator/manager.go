package operator

import (
	"context"
	"fmt"
	"net"
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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	secretsv1alpha1 "github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	inmemoryCache "github.com/Infisical/infisical/k8-operator/internal/cache"
	controllerv1beta1 "github.com/Infisical/infisical/k8-operator/internal/controller/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/template"
)

type Manager struct {
	cancel         context.CancelFunc
	client         client.Client
	metricsAddress string
}

func (m *Manager) Client() client.Client    { return m.client }
func (m *Manager) MetricsAddress() string    { return m.metricsAddress }

func (m *Manager) Stop() {
	m.cancel()
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

func InstallCRDs() error {
	cmd := exec.Command("make", "install")
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("make install: %s: %w", out, err)
	}

	return waitForCRDs(30*time.Second, []string{
		"infisicalconnections.secrets.infisical.com",
		"infisicalauths.secrets.infisical.com",
		"infisicalstaticsecrets.secrets.infisical.com",
	})
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

func Start(infisicalHostAPI string) (*Manager, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(secretsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(secretsv1beta1.AddToScheme(scheme))

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log

	template.InitializeTemplateFunctions()

	metricsAddr, err := freeAddress()
	if err != nil {
		return nil, fmt.Errorf("find free port for metrics: %w", err)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		return nil, fmt.Errorf("create manager: %w", err)
	}

	authCache, err := inmemoryCache.NewAuthCache(
		inmemoryCache.WithMinTTLThreshold(10 * time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("create auth cache: %w", err)
	}

	authResolver := auth.NewAuthStrategyResolver(mgr.GetClient(), authCache, logger, false)

	if err := (&controllerv1beta1.InfisicalConnectionReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		BaseLogger: logger,
	}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("setup InfisicalConnection controller: %w", err)
	}

	if err := (&controllerv1beta1.InfisicalAuthReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		BaseLogger:   logger,
		AuthResolver: authResolver,
	}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("setup InfisicalAuth controller: %w", err)
	}

	if err := (&controllerv1beta1.InfisicalStaticSecretReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		BaseLogger:   logger,
		AuthResolver: authResolver,
	}).SetupWithManager(mgr); err != nil {
		return nil, fmt.Errorf("setup InfisicalStaticSecret controller: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := mgr.Start(ctx); err != nil {
			logger.Error(err, "manager exited with error")
		}
		authCache.Cleanup()
	}()

	// Wait for caches to sync
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		cancel()
		return nil, fmt.Errorf("timed out waiting for cache sync")
	}

	return &Manager{
		cancel:         cancel,
		client:         mgr.GetClient(),
		metricsAddress: metricsAddr,
	}, nil
}

func freeAddress() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := l.Addr().String()
	l.Close()
	return addr, nil
}
