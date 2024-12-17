/*
Copyright 2021.

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

package main

import (
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/aws/aws-sdk-go/aws"
	awsclientgo "github.com/aws/aws-sdk-go/aws/client"
	awsiam "github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	eks "sigs.k8s.io/cluster-api-provider-aws/v2/controlplane/eks/api/v1beta2"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capa "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	expcapa "sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/v1beta2"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/giantswarm/capa-iam-operator/controllers"
	"github.com/giantswarm/capa-iam-operator/pkg/awsclient"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = capi.AddToScheme(scheme)
	_ = capa.AddToScheme(scheme)
	_ = eks.AddToScheme(scheme)
	_ = expcapa.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableKiamRole bool
	var enableIRSARole bool
	var enableLeaderElection bool
	var enableRoute53Role bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableKiamRole, "enable-kiam-role", true,
		"Enable creation and management of KIAM role for kiam app.")
	flag.BoolVar(&enableIRSARole, "enable-irsa-role", true,
		"Enable creation and management of IRSA role for irsa app.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableRoute53Role, "enable-route53-role", true,
		"Enable creation and management of Route53 role for external-dns app.")
	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(
			webhook.Options{
				Port: 9443,
			},
		),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "e3428bb4.giantswarm.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	awsClientAwsMachineTemplate, err := awsclient.New(awsclient.AWSClientConfig{
		CtrlClient: mgr.GetClient(),
		Log:        ctrl.Log.WithName("controllers").WithName("AWSMachineTemplate"),
	})
	if err != nil {
		setupLog.Error(err, "unable to create aws client for controller", "controller", "AWSMachineTemplate")
		os.Exit(1)
	}

	iamClientFactory := func(session awsclientgo.ConfigProvider, region string) iamiface.IAMAPI {
		return awsiam.New(session, &aws.Config{Region: aws.String(region)})
	}

	if err = (&controllers.AWSMachineTemplateReconciler{
		Client:            mgr.GetClient(),
		EnableKiamRole:    enableKiamRole,
		EnableRoute53Role: enableRoute53Role,
		AWSClient:         awsClientAwsMachineTemplate,
		IAMClientFactory:  iamClientFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AWSMachineTemplate")
		os.Exit(1)
	}

	awsClientAwsMachine, err := awsclient.New(awsclient.AWSClientConfig{
		CtrlClient: mgr.GetClient(),
		Log:        ctrl.Log.WithName("controllers").WithName("AWSMachinePool"),
	})
	if err != nil {
		setupLog.Error(err, "unable to create aws client for controller", "controller", "AWSMachinePool")
		os.Exit(1)
	}

	if err = (&controllers.AWSMachinePoolReconciler{
		Client:           mgr.GetClient(),
		AWSClient:        awsClientAwsMachine,
		IAMClientFactory: iamClientFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AWSMachinePool")
		os.Exit(1)
	}

	if err = (&controllers.AWSManagedControlPlaneReconciler{
		Client:           mgr.GetClient(),
		AWSClient:        awsClientAwsMachine,
		IAMClientFactory: iamClientFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AWSManagedControlPlane")
		os.Exit(1)
	}

	if err = (&controllers.POCReconciler{
		K8sClient: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AWSManagedControlPlane")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
