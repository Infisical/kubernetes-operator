package infra

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

const licenseFeaturePath = "/backend/dist/ee/services/license/license-fns.mjs"

type Builder struct {
	wantPostgres bool
	wantRedis    bool
	wantNodeJS   bool
	nodeJSFiles  []testcontainers.ContainerFile
	eeFeatures   []string
}

func New() *Builder { return &Builder{} }

func (b *Builder) WithPostgres() *Builder { b.wantPostgres = true; return b }

func (b *Builder) WithRedis() *Builder { b.wantRedis = true; return b }

func (b *Builder) WithNodeJSApi() *Builder {
	b.wantNodeJS = true
	b.wantPostgres = true
	b.wantRedis = true
	return b
}

func (b *Builder) WithNodeJSFile(file testcontainers.ContainerFile) *Builder {
	b.nodeJSFiles = append(b.nodeJSFiles, file)
	return b
}

func (b *Builder) WithEEFeatures(features ...string) *Builder {
	b.eeFeatures = append(b.eeFeatures, features...)
	return b
}

func (b *Builder) buildNodeJSCmd() []string {
	if len(b.eeFeatures) == 0 {
		return nil
	}

	var sedExprs []string
	for _, feature := range b.eeFeatures {
		sedExprs = append(sedExprs, fmt.Sprintf("s/%s: false/%s: true/g", feature, feature))
	}

	sedCmd := fmt.Sprintf("sed -i '%s' %s", strings.Join(sedExprs, "; "), licenseFeaturePath)
	return []string{"sh", "-c", sedCmd + " && ./standalone-entrypoint.sh"}
}

func (b *Builder) MustStart() *Stack {
	ctx := context.Background()

	net, err := network.New(ctx)
	if err != nil {
		log.Fatalf("infra: create network: %v", err)
	}

	stack := &Stack{network: net}

	var wg sync.WaitGroup
	var pgErr, redisErr error

	if b.wantPostgres {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stack.postgres, pgErr = startPostgres(ctx, net.Name)
		}()
	}

	if b.wantRedis {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stack.redis, redisErr = startRedis(ctx, net.Name)
		}()
	}

	wg.Wait()

	if pgErr != nil {
		log.Fatalf("infra: %v", pgErr)
	}
	if redisErr != nil {
		log.Fatalf("infra: %v", redisErr)
	}

	if b.wantNodeJS {
		stack.nodejs, err = startNodeJS(ctx, net.Name, b.nodeJSFiles, b.buildNodeJSCmd())
		if err != nil {
			log.Fatalf("infra: %v", err)
		}
	}

	if b.wantNodeJS {
		stack.nodejs.bootstrap()
	}

	log.Println("infra: stack ready")
	return stack
}
