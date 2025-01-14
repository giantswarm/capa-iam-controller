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

package controllers

import (
	"context"
	"fmt"

	awsclientgo "github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/giantswarm/microerror"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/types"
	capa "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/capa-iam-operator/pkg/awsclient"
	"github.com/giantswarm/capa-iam-operator/pkg/iam"
	"github.com/giantswarm/capa-iam-operator/pkg/key"
)

// AWSMachineTemplateReconciler reconciles a AWSMachineTemplate object
type AWSMachineTemplateReconciler struct {
	client.Client
	EnableKiamRole    bool
	EnableRoute53Role bool
	AWSClient         awsclient.AwsClientInterface
	IAMClientFactory  func(awsclientgo.ConfigProvider, string) iamiface.IAMAPI
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=awsmachinetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=awsmachinetemplates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=awsmachinetemplates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *AWSMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	awsMachineTemplate := &capa.AWSMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, awsMachineTemplate); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// check if CR got CAPI watch-filter label
	if !key.HasCapiWatchLabel(awsMachineTemplate.Labels) {
		logger.Info(fmt.Sprintf("AWSMachineTemplate do not have %s=%s label, ignoring CR", key.ClusterWatchFilterLabel, "capi"))
		// ignoring this CR
		return ctrl.Result{}, nil
	}

	var role string
	// check if there is control-plane or bastion role label on CR
	if key.IsControlPlaneAWSMachineTemplate(awsMachineTemplate.Labels) {
		role = iam.ControlPlaneRole
	} else if key.IsBastionAWSMachineTemplate(awsMachineTemplate.Labels) {
		role = iam.BastionRole
	} else {
		logger.Info(fmt.Sprintf("AWSMachineTemplate do not have %s=%s or %s=%s label, ignoring CR", key.ClusterRole, iam.ControlPlaneRole, key.ClusterRole, iam.BastionRole))
		// ignoring this CR
		return ctrl.Result{}, nil
	}
	clusterName, err := key.GetClusterIDFromLabels(awsMachineTemplate.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to get cluster name from AWSMachineTemplate")
	}

	logger = logger.WithValues("cluster", clusterName, "role", role)
	ctx = log.IntoContext(ctx, logger)

	if awsMachineTemplate.Spec.Template.Spec.IAMInstanceProfile == "" {
		logger.Info("AWSMachineTemplate has empty .Spec.Template.Spec.IAMInstanceProfile, not creating IAM role")
		return ctrl.Result{}, nil
	}

	awsCluster, err := key.GetAWSClusterByName(ctx, r.Client, clusterName, req.Namespace)
	if err != nil {
		return ctrl.Result{}, microerror.Mask(err)
	}
	awsClusterRoleIdentity, err := key.GetAWSClusterRoleIdentity(ctx, r.Client, awsCluster.Spec.IdentityRef.Name)
	if err != nil {
		logger.Error(err, "could not get AWSClusterRoleIdentity")
		return ctrl.Result{}, microerror.Mask(err)
	}

	awsClientSession, err := r.AWSClient.GetAWSClientSession(awsClusterRoleIdentity.Spec.RoleArn, awsCluster.Spec.Region)
	if err != nil {
		logger.Error(err, "Failed to get aws client session")
		return ctrl.Result{}, err
	}

	var iamService *iam.IAMService
	{
		c := iam.IAMServiceConfig{
			AWSSession:       awsClientSession,
			ClusterName:      clusterName,
			MainRoleName:     awsMachineTemplate.Spec.Template.Spec.IAMInstanceProfile,
			Log:              logger,
			RoleType:         role,
			Region:           awsCluster.Spec.Region,
			IAMClientFactory: r.IAMClientFactory,
			CustomTags:       awsCluster.Spec.AdditionalTags,
		}
		iamService, err = iam.New(c)
		if err != nil {
			logger.Error(err, "Failed to generate IAM service")
			return ctrl.Result{}, err
		}
	}

	if awsMachineTemplate.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, iamService, awsMachineTemplate, clusterName, req.Namespace, role)
	}
	return r.reconcileNormal(ctx, iamService, awsMachineTemplate, awsCluster, clusterName, role)
}

func (r *AWSMachineTemplateReconciler) reconcileDelete(ctx context.Context, iamService *iam.IAMService, awsMachineTemplate *capa.AWSMachineTemplate, clusterName, namespace, role string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	roleUsed, err := isRoleUsedElsewhere(ctx, r.Client, awsMachineTemplate.Spec.Template.Spec.IAMInstanceProfile)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !roleUsed {
		err = iamService.DeleteRole()
		if err != nil {
			return ctrl.Result{}, err
		}
		if role == iam.ControlPlaneRole {
			if r.EnableRoute53Role {
				err = iamService.DeleteRolesForIRSA()
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}
	// remove finalizer from AWSCluster
	awsCluster, err := key.GetAWSClusterByName(ctx, r.Client, clusterName, awsMachineTemplate.GetNamespace())
	if err != nil {
		logger.Error(err, "failed to get awsCluster")
		return ctrl.Result{}, err
	}
	err = removeFinalizer(ctx, r.Client, awsCluster, iam.ControlPlaneRole)
	if err != nil {
		logger.Error(err, "Failed to remove finalizer from AWSCluster")
		return ctrl.Result{}, err
	}

	// remove finalizer from AWSMachineTemplate
	err = removeFinalizer(ctx, r.Client, awsMachineTemplate, iam.ControlPlaneRole)
	if err != nil {
		logger.Error(err, "Failed to remove finalizer from AWSMachineTemplate")
		return ctrl.Result{}, err
	}

	cm := &corev1.ConfigMap{}
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-%s", clusterName, "cluster-values"),
		},
		cm)
	if err != nil {
		logger.Error(err, "Failed to get the cluster-values configmap for cluster")
		return ctrl.Result{}, errors.WithStack(client.IgnoreNotFound(err))
	}

	err = removeFinalizer(ctx, r.Client, cm, iam.ControlPlaneRole)
	if err != nil {
		logger.Error(err, "Failed to remove finalizer from ConfigMap")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AWSMachineTemplateReconciler) reconcileNormal(ctx context.Context, iamService *iam.IAMService, awsMachineTemplate *capa.AWSMachineTemplate, awsCluster *capa.AWSCluster, clusterName, role string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// add finalizer to AWSMachineTemplate
	if !controllerutil.ContainsFinalizer(awsMachineTemplate, key.FinalizerName(iam.ControlPlaneRole)) {
		patchHelper, err := patch.NewHelper(awsMachineTemplate, r.Client)
		if err != nil {
			return ctrl.Result{}, errors.WithStack(err)
		}
		controllerutil.AddFinalizer(awsMachineTemplate, key.FinalizerName(iam.ControlPlaneRole))
		err = patchHelper.Patch(ctx, awsMachineTemplate)
		if err != nil {
			logger.Error(err, "failed to add finalizer on AWSMachineTemplate")
			return ctrl.Result{}, errors.WithStack(err)
		}
		logger.Info("successfully added finalizer to AWSMachineTemplate", "finalizer_name", key.FinalizerName(iam.ControlPlaneRole))
	}

	// add finalizer to AWSCluster
	if !controllerutil.ContainsFinalizer(awsCluster, key.FinalizerName(iam.ControlPlaneRole)) {
		patchHelper, err := patch.NewHelper(awsCluster, r.Client)
		if err != nil {
			return ctrl.Result{}, errors.WithStack(err)
		}
		controllerutil.AddFinalizer(awsCluster, key.FinalizerName(iam.ControlPlaneRole))
		err = patchHelper.Patch(ctx, awsCluster)
		if err != nil {
			logger.Error(err, "failed to add finalizer on AWSCluster")
			return ctrl.Result{}, errors.WithStack(err)
		}
		logger.Info("successfully added finalizer to AWSCluster", "finalizer_name", key.FinalizerName(iam.ControlPlaneRole))
	}

	err := iamService.ReconcileRole()
	if err != nil {
		return ctrl.Result{}, err
	}

	if role == iam.ControlPlaneRole {
		// route53 role depends on KIAM role
		if r.EnableRoute53Role {
			logger.Info("reconciling IRSA roles")
			identityRefName := awsCluster.Spec.IdentityRef.Name
			awsClusterRoleIdentity, err := key.GetAWSClusterRoleIdentity(ctx, r.Client, identityRefName)
			if err != nil {
				logger.Error(err, "could not get AWSClusterRoleIdentity")
				return ctrl.Result{}, errors.WithStack(err)
			}

			accountID, err := key.GetAWSAccountID(awsClusterRoleIdentity)
			if err != nil {
				logger.Error(err, "Could not get account ID")
				return ctrl.Result{}, errors.WithStack(err)
			}

			baseDomain, err := key.GetBaseDomain(ctx, r.Client, clusterName, awsCluster.Namespace)
			if err != nil {
				logger.Error(err, "Could not get base domain")
				return ctrl.Result{}, errors.WithStack(err)
			}

			irsaDomain := key.IRSADomain(baseDomain, awsCluster.Spec.Region, accountID, clusterName)

			irsaTrustDomains := key.GetIRSATrustDomains(awsMachineTemplate, awsCluster, irsaDomain)

			err = iamService.ReconcileRolesForIRSA(accountID, irsaTrustDomains)
			if err != nil {
				return ctrl.Result{}, errors.WithStack(err)
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AWSMachineTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capa.AWSMachineTemplate{}).
		Complete(r)
}
