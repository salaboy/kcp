/*
Copyright 2022 The KCP Authors.

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

package cache

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"testing"

	kcpclienthelper "github.com/kcp-dev/apimachinery/pkg/client"
	"github.com/stretchr/testify/require"

	apimachineryerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	cacheclient "github.com/kcp-dev/kcp/pkg/cache/client"
	"github.com/kcp-dev/kcp/pkg/cache/client/shard"
	cacheserver "github.com/kcp-dev/kcp/pkg/cache/server"
	cacheopitons "github.com/kcp-dev/kcp/pkg/cache/server/options"
	"github.com/kcp-dev/kcp/pkg/embeddedetcd"
	"github.com/kcp-dev/kcp/test/e2e/framework"
)

// StartStandaloneCacheServer runs the cache server as a separate process
// and returns a path to kubeconfig that can be used to communicate with the server.
func StartStandaloneCacheServer(ctx context.Context, t *testing.T, dataDir string) string {
	cacheServerPortStr, err := framework.GetFreePort(t)
	require.NoError(t, err)
	cacheServerPort, err := strconv.Atoi(cacheServerPortStr)
	require.NoError(t, err)
	cacheServerOptions := cacheopitons.NewOptions(path.Join(dataDir, "cache"))
	cacheServerOptions.SecureServing.BindPort = cacheServerPort
	cacheServerEmbeddedEtcdClientPort, err := framework.GetFreePort(t)
	require.NoError(t, err)
	cacheServerEmbeddedEtcdPeerPort, err := framework.GetFreePort(t)
	require.NoError(t, err)
	cacheServerOptions.EmbeddedEtcd.ClientPort = cacheServerEmbeddedEtcdClientPort
	cacheServerOptions.EmbeddedEtcd.PeerPort = cacheServerEmbeddedEtcdPeerPort
	cacheServerCompletedOptions, err := cacheServerOptions.Complete()
	require.NoError(t, err)
	if errs := cacheServerCompletedOptions.Validate(); len(errs) > 0 {
		require.NoError(t, apimachineryerrors.NewAggregate(errs))
	}
	cacheServerConfig, err := cacheserver.NewConfig(cacheServerCompletedOptions, nil)
	require.NoError(t, err)
	cacheServerCompletedConfig, err := cacheServerConfig.Complete()
	require.NoError(t, err)

	if cacheServerCompletedConfig.EmbeddedEtcd.Config != nil {
		t.Logf("Starting embedded etcd for the cache server")
		require.NoError(t, embeddedetcd.NewServer(cacheServerCompletedConfig.EmbeddedEtcd).Run(ctx))
	}
	cacheServer, err := cacheserver.NewServer(cacheServerCompletedConfig)
	require.NoError(t, err)
	preparedCachedServer, err := cacheServer.PrepareRun(ctx)
	require.NoError(t, err)
	t.Logf("Starting the cache server")
	go func() {
		// TODO (p0lyn0mial): check readiness of the cache server
		require.NoError(t, preparedCachedServer.Run(ctx))
	}()
	t.Logf("Creating kubeconfig for the cache server at %s", dataDir)
	cacheServerKubeConfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"cache": {
				Server:               fmt.Sprintf("https://localhost:%s", cacheServerPortStr),
				CertificateAuthority: path.Join(dataDir, "cache", "apiserver.crt"),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"cache": {
				Cluster: "cache",
			},
		},
		CurrentContext: "cache",
	}
	cacheKubeconfigPath := filepath.Join(dataDir, "cache", "cache.kubeconfig")
	err = clientcmd.WriteToFile(cacheServerKubeConfig, cacheKubeconfigPath)
	require.NoError(t, err)
	return cacheKubeconfigPath
}

func CacheClientRoundTrippersFor(cfg *rest.Config) *rest.Config {
	cacheClientRT := cacheclient.WithCacheServiceRoundTripper(rest.CopyConfig(cfg))
	cacheClientRT = cacheclient.WithShardNameFromContextRoundTripper(cacheClientRT)
	cacheClientRT = cacheclient.WithDefaultShardRoundTripper(cacheClientRT, shard.Wildcard)
	kcpclienthelper.SetMultiClusterRoundTripper(cacheClientRT)
	return cacheClientRT
}
