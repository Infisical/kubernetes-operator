package infra

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const nginxConf = `
events {}
http {
    server {
        listen 443 ssl;
        ssl_certificate     /etc/nginx/certs/server.crt;
        ssl_certificate_key /etc/nginx/certs/server.key;

        location / {
            proxy_pass http://backend-nodejs:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto https;
            proxy_buffering off;
            proxy_cache off;
        }
    }
}
`

type NginxService struct {
	container    testcontainers.Container
	url          string
	inClusterURL string
	kindIP       string
}

func (n *NginxService) URL() string          { return n.url }
func (n *NginxService) InClusterURL() string { return n.inClusterURL }
func (n *NginxService) KindIP() string       { return n.kindIP }

func startNginx(ctx context.Context, networkName string, tls *TLSBundle) (*NginxService, error) {
	req := testcontainers.ContainerRequest{
		Image:        "nginx:alpine",
		ExposedPorts: []string{"443/tcp"},
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"tls-proxy"},
		},
		Files: []testcontainers.ContainerFile{
			{
				ContainerFilePath: "/etc/nginx/nginx.conf",
				Reader:            stringReader(nginxConf),
				FileMode:          0o644,
			},
			{
				ContainerFilePath: "/etc/nginx/certs/server.crt",
				Reader:            bytesReader(tls.ServerCert),
				FileMode:          0o644,
			},
			{
				ContainerFilePath: "/etc/nginx/certs/server.key",
				Reader:            bytesReader(tls.ServerKey),
				FileMode:          0o600,
			},
		},
		WaitingFor: wait.ForListeningPort("443/tcp").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting nginx: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting nginx host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "443/tcp")
	if err != nil {
		return nil, fmt.Errorf("getting nginx port: %w", err)
	}

	url := fmt.Sprintf("https://%s:%d", host, mappedPort.Int())

	return &NginxService{
		container: container,
		url:       url,
	}, nil
}

func (n *NginxService) connectToKindNetwork() (string, error) {
	kindNetwork := "kind"
	containerID := n.container.GetContainerID()

	out, err := exec.Command("docker", "network", "connect", kindNetwork, containerID).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker network connect: %s: %w", string(out), err)
	}

	inspectOut, err := exec.Command("docker", "inspect",
		"--format", fmt.Sprintf("{{(index .NetworkSettings.Networks %q).IPAddress}}", kindNetwork),
		containerID,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker inspect: %s: %w", string(inspectOut), err)
	}

	ip := strings.TrimSpace(string(inspectOut))
	if ip == "" {
		return "", fmt.Errorf("no IP found for container on network %q", kindNetwork)
	}

	n.kindIP = ip
	n.inClusterURL = fmt.Sprintf("https://%s:443", ip)
	return ip, nil
}

func (n *NginxService) replaceCerts(tls *TLSBundle) error {
	containerID := n.container.GetContainerID()

	if err := dockerWrite(tls.ServerCert, containerID, "/etc/nginx/certs/server.crt"); err != nil {
		return fmt.Errorf("copy server cert: %w", err)
	}
	if err := dockerWrite(tls.ServerKey, containerID, "/etc/nginx/certs/server.key"); err != nil {
		return fmt.Errorf("copy server key: %w", err)
	}

	out, err := exec.Command("docker", "exec", containerID, "nginx", "-s", "reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx reload: %s: %w", string(out), err)
	}
	return nil
}

func dockerWrite(data []byte, containerID, destPath string) error {
	cmd := exec.Command("docker", "exec", "-i", containerID, "sh", "-c", fmt.Sprintf("cat > %s", destPath))
	cmd.Stdin = bytesReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}
