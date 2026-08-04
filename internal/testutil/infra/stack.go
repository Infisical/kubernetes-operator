package infra

import (
	"context"
	"fmt"
	"log"

	"github.com/testcontainers/testcontainers-go"
)

type Stack struct {
	postgres *PostgresService
	redis    *RedisService
	nodejs   *NodeJSService
	nginx    *NginxService
	tls      *TLSBundle
	network  *testcontainers.DockerNetwork
}

func (s *Stack) Postgres() *PostgresService { return s.postgres }
func (s *Stack) Redis() *RedisService       { return s.redis }
func (s *Stack) NodeJS() *NodeJSService     { return s.nodejs }
func (s *Stack) Nginx() *NginxService       { return s.nginx }
func (s *Stack) TLS() *TLSBundle            { return s.tls }

func (s *Stack) NetworkName() string { return s.network.Name }

func (s *Stack) StartTLSProxy(opts TLSBundleOpts) error {
	tlsBundle, err := GenerateTLSBundle(opts)
	if err != nil {
		return fmt.Errorf("generate TLS bundle: %w", err)
	}
	s.tls = tlsBundle

	ctx := context.Background()
	nginx, err := startNginx(ctx, s.network.Name, tlsBundle)
	if err != nil {
		return fmt.Errorf("start nginx: %w", err)
	}
	s.nginx = nginx
	return nil
}

func (s *Stack) Stop() {
	ctx := context.Background()

	if s.nginx != nil {
		if err := s.nginx.container.Terminate(ctx); err != nil {
			log.Printf("infra.Stop: terminate nginx: %v", err)
		}
	}
	if s.nodejs != nil {
		if err := s.nodejs.container.Terminate(ctx); err != nil {
			log.Printf("infra.Stop: terminate nodejs: %v", err)
		}
	}
	if s.redis != nil {
		if err := s.redis.container.Terminate(ctx); err != nil {
			log.Printf("infra.Stop: terminate redis: %v", err)
		}
	}
	if s.postgres != nil {
		if err := s.postgres.container.Terminate(ctx); err != nil {
			log.Printf("infra.Stop: terminate postgres: %v", err)
		}
	}
	if s.network != nil {
		if err := s.network.Remove(ctx); err != nil {
			log.Printf("infra.Stop: remove network: %v", err)
		}
	}
}
