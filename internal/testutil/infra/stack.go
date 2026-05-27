package infra

import (
	"context"
	"log"

	"github.com/testcontainers/testcontainers-go"
)

type Stack struct {
	postgres *PostgresService
	redis    *RedisService
	nodejs   *NodeJSService
	network  *testcontainers.DockerNetwork
}

func (s *Stack) Postgres() *PostgresService { return s.postgres }
func (s *Stack) Redis() *RedisService       { return s.redis }
func (s *Stack) NodeJS() *NodeJSService     { return s.nodejs }

func (s *Stack) Stop() {
	ctx := context.Background()

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
