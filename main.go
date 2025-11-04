package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexflint/go-arg"

	"github.com/confighub/sdk/worker"
	"github.com/fluxcd/pkg/artifact/config"
	"github.com/fluxcd/pkg/artifact/server"
	gotkstorage "github.com/fluxcd/pkg/artifact/storage"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	klog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/confighubai/flux-bridge/internal/bridge"
	"github.com/confighubai/flux-bridge/internal/controller"
)

type Arguments struct {
	Addr         string `arg:"--addr,env:ADDR" default:":8080"`
	DataDir      string `arg:"--data-dir,env:DATA_DIR"`
	Namespace    string `arg:"--namespace,env:NAMESPACE,required"`
	WorkerName   string `arg:"--name,env:CONFIGHUB_WORKER_NAME" default:"flux-bridge-poc"`
	WorkerID     string `arg:"--worker-id,env:CONFIGHUB_WORKER_ID,required"`
	WorkerSecret string `arg:"--worker-secret,env:CONFIGHUB_WORKER_SECRET,required"`
	ConfigHubURL string `arg:"--confighub-url,env:CONFIGHUB_URL" default:"https://hub.confighub.com"`
}

func main() {
	args := &Arguments{}
	arg.MustParse(args)

	err := run(*args)
	if err != nil {
		fmt.Println("flux-bridge shutdown with error", err)
		os.Exit(1)
	}
	fmt.Println("flux-bridge shutdown, gracefully")
}

func run(args Arguments) error {
	klog.SetLogger(logr.Discard())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	g, gCtx := errgroup.WithContext(ctx)

	// Setup Flux client.
	kubeCfg, err := kconfig.GetConfig()
	if err != nil {
		return err
	}
	kubeClient, err := client.New(kubeCfg, client.Options{})
	if err != nil {
		return err
	}

	dataDir := args.DataDir
	if dataDir == "" {
		tmpDir, err := os.MkdirTemp("", args.WorkerName+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		dataDir = tmpDir
	}
	artifactCfg := &config.Options{
		StoragePath:       dataDir,
		StorageAddress:    args.Addr,
		StorageAdvAddress: fmt.Sprintf("flux-bridge.%s.svc.cluster.local.", args.Namespace),
	}
	storage, err := gotkstorage.New(artifactCfg)
	if err != nil {
		return err
	}
	g.Go(func() error {
		return server.Start(gCtx, artifactCfg)
	})
	fluxCtrl, err := controller.NewFluxController(ctx, storage, kubeClient, args.Namespace)
	if err != nil {
		return err
	}

	// ConfigHub fluxBridge and worker.
	fluxBridge, err := bridge.NewFluxBridge(fluxCtrl, args.WorkerName)
	if err != nil {
		return fmt.Errorf("could not create Flux bridge: %w", err)
	}
	bridgeDispatcher := worker.NewBridgeDispatcher()
	bridgeDispatcher.RegisterBridge(fluxBridge)
	connector, err := worker.NewConnector(worker.ConnectorOptions{
		WorkerID:         args.WorkerID,
		WorkerSecret:     args.WorkerSecret,
		ConfigHubURL:     args.ConfigHubURL,
		BridgeDispatcher: &bridgeDispatcher,
	})
	if err != nil {
		return fmt.Errorf("could not create connector: %w", err)
	}
	g.Go(func() error {
		err := connector.Start()
		if err != nil {
			return fmt.Errorf("connector run error: %w", err)
		}
		return nil
	})

	// Artifact file server.
	err = g.Wait()
	if err != nil {
		return err
	}
	return nil
}
