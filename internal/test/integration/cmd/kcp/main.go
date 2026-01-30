package main

import (
	"os"

	setup "github.com/platform-mesh/security-operator/internal/test/integration"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("kcpsetup")

	ctx := ctrl.SetupSignalHandler()

	if err := setup.KcpSetup(ctx, ""); err != nil {
		log.Error(err, "failed to configure kcp")
		os.Exit(1)
	}

	log.Info("kcp setup completed; starting manager")
	if err := setup.RunPredicateManager(ctx, "", ctrl.Log.WithName("predicate")); err != nil {
		log.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
