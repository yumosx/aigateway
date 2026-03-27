package main

import (
	"flag"
	"log"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/aegisflow/aegisflow/internal/operator"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

func main() {
	namespace := flag.String("namespace", "default", "namespace to watch")
	metricsAddr := flag.String("metrics-bind-address", ":8090", "metrics endpoint")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: *metricsAddr,
		},
	})
	if err != nil {
		log.Fatalf("unable to create manager: %v", err)
		os.Exit(1)
	}

	reconciler := operator.NewReconciler(mgr.GetClient(), *namespace)
	_ = reconciler // Will be wired to controller watches when CRD scheme registration is added

	log.Printf("aegisflow-operator starting (namespace: %s)", *namespace)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("unable to start manager: %v", err)
		os.Exit(1)
	}
}
