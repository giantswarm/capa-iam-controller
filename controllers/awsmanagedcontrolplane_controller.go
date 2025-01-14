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
	"time"

	awsclientgo "github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/giantswarm/microerror"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	eks "sigs.k8s.io/cluster-api-provider-aws/v2/controlplane/eks/api/v1beta2"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/capa-iam-operator/pkg/awsclient"
	"github.com/giantswarm/capa-iam-operator/pkg/iam"
	"github.com/giantswarm/capa-iam-operator/pkg/key"
)

// AWSManagedControlPlaneReconciler reconciles a AWSManagedControlPlane object
type AWSManagedControlPlaneReconciler struct {
	client.Client
	AWSClient        awsclient.AwsClientInterface
	IAMClientFactory func(awsclientgo.ConfigProvider, string) iamiface.IAMAPI
}

func (r *AWSManagedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	eksCluster := &eks.AWSManagedControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, eksCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, microerror.Mask(err)
	}

	clusterName, err := key.GetClusterIDFromLabels(eksCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, microerror.Mask(err)
	}

	if eksCluster.Spec.RoleName == nil {
		logger.Info("AWSManagedControlPlane has empty .spec.RoleName, waiting for role creation")
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: time.Minute,
		}, nil
	}

	awsClusterRoleIdentity, err := key.GetAWSClusterRoleIdentity(ctx, r.Client, eksCluster.Spec.IdentityRef.Name)
	if err != nil {
		logger.Error(err, "could not get AWSClusterRoleIdentity")
		return ctrl.Result{}, microerror.Mask(err)
	}

	awsClientSession, err := r.AWSClient.GetAWSClientSession(awsClusterRoleIdentity.Spec.RoleArn, eksCluster.Spec.Region)
	if err != nil {
		logger.Error(err, "Failed to get aws client session")
		return ctrl.Result{}, microerror.Mask(err)
	}

	var iamService *iam.IAMService
	{
		c := iam.IAMServiceConfig{
			AWSSession:       awsClientSession,
			ClusterName:      clusterName,
			MainRoleName:     *eksCluster.Spec.RoleName,
			Log:              logger,
			RoleType:         iam.IRSARole,
			Region:           eksCluster.Spec.Region,
			IAMClientFactory: r.IAMClientFactory,
			CustomTags:       eksCluster.Spec.AdditionalTags,
		}
		iamService, err = iam.New(c)
		if err != nil {
			logger.Error(err, "Failed to generate IAM service")
			return ctrl.Result{}, microerror.Mask(err)
		}
	}

	if eksCluster.DeletionTimestamp != nil {
		err = iamService.DeleteRolesForIRSA()
		if err != nil {
			return ctrl.Result{}, microerror.Mask(err)
		}

		err = removeFinalizer(ctx, r.Client, eksCluster, iam.IRSARole)
		if err != nil {
			logger.Error(err, "failed to remove finalizer on AWSManagedControlPlane")
			return ctrl.Result{}, microerror.Mask(err)
		}

	} else {
		// add finalizer to AWSManagedControlPlane
		if !controllerutil.ContainsFinalizer(eksCluster, key.FinalizerName(iam.IRSARole)) {
			patchHelper, err := patch.NewHelper(eksCluster, r.Client)
			if err != nil {
				return ctrl.Result{}, microerror.Mask(err)
			}
			controllerutil.AddFinalizer(eksCluster, key.FinalizerName(iam.IRSARole))
			err = patchHelper.Patch(ctx, eksCluster)
			if err != nil {
				logger.Error(err, "failed to add finalizer on AWSManagedControlPlane")
				return ctrl.Result{}, microerror.Mask(err)
			}
			logger.Info("successfully added finalizer to AWSManagedControlPlane", "finalizer_name", key.FinalizerName(iam.IRSARole))
		}

		accountID, err := key.GetAWSAccountID(awsClusterRoleIdentity)
		if err != nil {
			logger.Error(err, "Could not get account ID")
			return ctrl.Result{}, microerror.Mask(err)
		}

		eksOpenIdDomain, err := iamService.GetIRSAOpenIDForEKS(eksCluster.Name)
		if err != nil {
			logger.Error(err, "failed to fetch EKS OpenConnectID URL")
			return ctrl.Result{}, microerror.Mask(err)
		}

		eksRoleARN, err := iamService.GetRoleARN(*eksCluster.Spec.RoleName)
		if err != nil {
			logger.Error(err, "failed to fetch EKS role name ARN")

			return ctrl.Result{}, microerror.Mask(err)
		}

		iamService.SetPrincipalRoleARN(eksRoleARN)
		err = iamService.ReconcileRolesForIRSA(accountID, []string{eksOpenIdDomain})
		if err != nil {
			return ctrl.Result{}, microerror.Mask(err)
		}
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: 5 * time.Minute,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AWSManagedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&eks.AWSManagedControlPlane{}).
		Complete(r)
}
