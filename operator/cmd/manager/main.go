// Copyright © 2019 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/banzaicloud/bank-vaults/operator/pkg/apis"
	"github.com/banzaicloud/bank-vaults/operator/pkg/controller"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/metrics"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

var log = logf.Log.WithName("cmd")

const (
	operatorNamespace = "OPERATOR_NAMESPACE"
	livenessPort      = "8080"
	metricsHost       = "0.0.0.0"
	metricsPort       = 8383
)

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("operator-sdk Version: %v", sdkVersion.Version))
}

func handleLiveness() {
	log.Info(fmt.Sprintf("Liveness probe listening on: %s", livenessPort))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.V(2).Info("ping")
	})
	err := http.ListenAndServe(":"+livenessPort, nil)
	if err != nil {
		log.Error(err, "failed to start health probe: %v\n")
	}
}

func main() {

	syncPeriod := flag.Duration("sync_period", 30*time.Second, "SyncPeriod determines the minimum frequency at which watched resources are reconciled")
	verbose := flag.Bool("verbose", false, "enable verbose logging")

	flag.Parse()

	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(logf.ZapLogger(*verbose))

	printVersion()

	var namespace string
	var err error
	namespace, isSet := os.LookupEnv(operatorNamespace)

	if !isSet {
		namespace, err = k8sutil.GetWatchNamespace()
		if err != nil {
			log.Info("No watched namespace found, watching the entire cluster")
			namespace = ""
		}
	}
	log.Info(fmt.Sprintf("Watched namespace: %s", namespace))

	// Get a config to talk to the apiserver
	k8sConfig, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Start the liveness probe handler
	go handleLiveness()

	// Become the leader before proceeding
	err = leader.Become(context.TODO(), "vault-operator-lock")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		log.V(2).Info("ready")
	})

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(k8sConfig, manager.Options{
		Namespace:          namespace,
		SyncPeriod:         syncPeriod,
		MetricsBindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort),
	})
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Expose Controller Metrics

	// Add to the below struct any other metrics ports you want to expose.
	servicePorts := []v1.ServicePort{
		{
			Port:       metricsPort,
			Name:       metrics.OperatorPortName,
			Protocol:   v1.ProtocolTCP,
			TargetPort: intstr.FromInt(metricsPort),
		},
	}

	_, err = metrics.CreateMetricsService(context.TODO(), k8sConfig, servicePorts)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited non-zero")
		os.Exit(1)
	}
}
