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
	networkName  string
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

// WithNetwork starts the containers on an existing Docker network instead of a
// dedicated one. The network is left in place when the stack stops.
func (b *Builder) WithNetwork(name string) *Builder { b.networkName = name; return b }

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

	stack := &Stack{}
	networkName := b.networkName
	if networkName == "" {
		net, err := network.New(ctx)
		if err != nil {
			log.Fatalf("infra: create network: %v", err)
		}
		stack.network = net
		networkName = net.Name
	}

	var wg sync.WaitGroup
	var pgErr, redisErr error

	if b.wantPostgres {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stack.postgres, pgErr = startPostgres(ctx, networkName)
		}()
	}

	if b.wantRedis {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stack.redis, redisErr = startRedis(ctx, networkName)
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
		var err error
		stack.nodejs, err = startNodeJS(ctx, networkName, b.nodeJSFiles, b.buildNodeJSCmd())
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
