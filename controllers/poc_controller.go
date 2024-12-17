package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go/aws"
	awssdk "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	awssession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	capa "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/microerror"

	"github.com/giantswarm/capa-iam-operator/pkg/key"
)

var iamRoleNames = []string{
	"ALBController-Role",
	"ebs-csi-driver-role",
	"efs-csi-driver-role",
	"cluster-autoscaler-role",
	"CertManager-Role",
	"Route53Manager-Role",
}

type POCReconciler struct {
	K8sClient client.Client
}

// "Effect": "Allow",
//
//	"Principal": {
//	  "Federated": "arn:{{ $.AWSDomain }}:iam::{{ $.AccountID }}:oidc-provider/{{ $domain }}"
//	},
//
// "Action": "sts:AssumeRoleWithWebIdentity",
//
//	"Condition": {
//	  "StringLike": {
//	    "{{ $domain }}:sub": "system:serviceaccount:*:{{ $.ServiceAccount }}"
//	  }
//	}

// {
// 	"Version": "2012-10-17",
// 	"Statement": [
// 		{
// 			"Effect": "Allow",
// 			"Principal": {
// 				"Federated": "arn:aws:iam::<REDACTED>:oidc-provider/irsa.<REDACTED>"
// 			},
// 			"Action": "sts:AssumeRoleWithWebIdentity",
// 			"Condition": {
// 				"StringEquals": {
// 					"irsa.<REDACTED>:sub": "system:serviceaccount:*:aws-load-balancer-controller"
// 				}
// 			}
// 		}
//     ]
// }

type Condition struct {
	StringLike   map[string]string `json:"StringLike,omitempty"`
	StringEquals map[string]string `json:"StringEquals,omitempty"`
}

type Principal struct {
	Federated string `json:"Federated,omitempty"`
}

type Statement struct {
	Effect    string    `json:"Effect,omitempty"`
	Principal Principal `json:"Principal,omitempty"`
	Action    string    `json:"Action,omitempty"`
	Condition Condition `json:"Condition,omitempty"`
}

type AssumeRolePolicy struct {
	Statement []Statement `json:"Statement,omitempty"`
}

func (r *POCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capa.AWSCluster{}).
		Complete(r)
}

func (r *POCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	awsCluster := &capa.AWSCluster{}
	err := r.K8sClient.Get(ctx, req.NamespacedName, awsCluster)
	if client.IgnoreNotFound(err) != nil {
		logger.Error(err, "failed to get AWSCluster")
		return ctrl.Result{}, nil
	}

	awsRoleIdentity := &capa.AWSClusterRoleIdentity{}
	err = r.K8sClient.Get(ctx, types.NamespacedName{
		Name: awsCluster.Spec.IdentityRef.Name,
	}, awsRoleIdentity)
	if err != nil {
		logger.Error(err, "failed to get AWSClusterRoleIdentity")
		return ctrl.Result{}, err
	}

	iamClient, err := r.newIAMCLient(awsRoleIdentity.Spec.RoleArn, awsCluster.Spec.Region)
	if err != nil {
		logger.Error(err, "failed to create IAM client")
		return ctrl.Result{}, err
	}

	accountID, err := key.GetAWSAccountID(awsRoleIdentity)
	if err != nil {
		logger.Error(err, "Could not get AWS account ID")
		return ctrl.Result{}, err
	}

	baseDomain, err := key.GetBaseDomain(ctx, r.K8sClient, awsCluster.Name, awsCluster.Namespace)
	if err != nil {
		logger.Error(err, "Could not get base domain")
		return ctrl.Result{}, errors.WithStack(err)
	}

	irsaDomain := key.IRSADomain(baseDomain, awsCluster.Spec.Region, accountID, awsCluster.Name)
	principal := fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, irsaDomain)

	if awsCluster.DeletionTimestamp != nil {
		logger.Info("Reconciling delete")
		return r.reconcileDelete(ctx, iamClient, awsCluster, principal)
	}

	return r.reconcileNormal(ctx, iamClient, awsCluster, irsaDomain, principal)
}

func (r *POCReconciler) reconcileNormal(ctx context.Context, iamClient *iam.IAM, awsCluster *capa.AWSCluster, irsaDomain, principal string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	patchHelper, err := patch.NewHelper(awsCluster, r.K8sClient)
	if err != nil {
		logger.Error(err, "failed to create patch helper")
		return ctrl.Result{}, errors.WithStack(err)
	}
	controllerutil.AddFinalizer(awsCluster, "capa-iam-operator.finalizers.giantswarm.io/poc")
	err = patchHelper.Patch(ctx, awsCluster)
	if err != nil {
		logger.Error(err, "failed to add finalizer on AWSMachinePool")
		return ctrl.Result{}, errors.WithStack(err)
	}

	for _, iamRole := range iamRoleNames {
		out, err := iamClient.GetRole(&iam.GetRoleInput{
			RoleName: &iamRole,
		})
		if err != nil {
			logger.Error(err, "failed to get IAM role", "role", iamRole)
			return ctrl.Result{}, errors.WithStack(err)
		}
		assumeRolePolicyDataURLEncoded := *out.Role.AssumeRolePolicyDocument
		assumeRolePolicyDataString, err := url.QueryUnescape(assumeRolePolicyDataURLEncoded)
		if err != nil {
			logger.Error(err, fmt.Sprintf("failed to url escape assume role policy: %s", assumeRolePolicyDataURLEncoded))
			return ctrl.Result{}, err
		}

		assumeRolePolicyData := []byte(assumeRolePolicyDataString)

		var assumeRolePolicy AssumeRolePolicy
		err = json.Unmarshal(assumeRolePolicyData, &assumeRolePolicy)
		if err != nil {
			logger.Error(err, fmt.Sprintf("failed to unmarshal assume role policy: %s", string(assumeRolePolicyData)))
			return ctrl.Result{}, err
		}
		assumeRolePolicy = r.addStatement(assumeRolePolicy, iamRole, irsaDomain, principal)
		assumeRolePolicyData, err = json.Marshal(assumeRolePolicy)
		if err != nil {
			logger.Error(err, fmt.Sprintf("failed to marshal assume role policy: %#+v", assumeRolePolicy))
			return ctrl.Result{}, err
		}

		_, err = iamClient.UpdateAssumeRolePolicy(&iam.UpdateAssumeRolePolicyInput{
			PolicyDocument: awssdk.String(string(assumeRolePolicyData)),
			RoleName:       awssdk.String(iamRole),
		})
		if err != nil {
			logger.Error(err, fmt.Sprintf("failed to update assume role policy: %s", string(assumeRolePolicyData)))
			return ctrl.Result{}, err
		}

	}

	return ctrl.Result{}, nil
}

func (r *POCReconciler) reconcileDelete(ctx context.Context, iamClient *iam.IAM, awsCluster *capa.AWSCluster, principal string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	patchHelper, err := patch.NewHelper(awsCluster, r.K8sClient)
	if err != nil {
		logger.Error(err, "failed to create patch helper")
		return ctrl.Result{}, errors.WithStack(err)
	}
	controllerutil.RemoveFinalizer(awsCluster, "capa-iam-operator.finalizers.giantswarm.io/poc")
	err = patchHelper.Patch(ctx, awsCluster)
	if err != nil {
		logger.Error(err, "failed to remove finalizer on AWSMachinePool")
		return ctrl.Result{}, errors.WithStack(err)
	}

	for _, iamRole := range iamRoleNames {
		out, err := iamClient.GetRole(&iam.GetRoleInput{
			RoleName: &iamRole,
		})
		if err != nil {
			logger.Error(err, "failed to get IAM role", "role", iamRole)
			return ctrl.Result{}, errors.WithStack(err)
		}
		assumeRolePolicyData := []byte(*out.Role.AssumeRolePolicyDocument)

		var assumeRolePolicy AssumeRolePolicy
		err = json.Unmarshal(assumeRolePolicyData, &assumeRolePolicy)
		if err != nil {
			logger.Error(err, "failed to unmarshal assume role policy")
			return ctrl.Result{}, err
		}
		assumeRolePolicy = r.removeStatement(assumeRolePolicy, iamRole, principal)
		assumeRolePolicyData, err = json.Marshal(assumeRolePolicy)
		if err != nil {
			logger.Error(err, "failed to marshal assume role policy")
			return ctrl.Result{}, err
		}

		_, err = iamClient.UpdateAssumeRolePolicy(&iam.UpdateAssumeRolePolicyInput{
			PolicyDocument: awssdk.String(string(assumeRolePolicyData)),
			RoleName:       awssdk.String(iamRole),
		})
		if err != nil {
			logger.Error(err, "failed to update assume role policy")
			return ctrl.Result{}, err
		}

	}

	return ctrl.Result{}, nil
}

func (r *POCReconciler) addStatement(assumeRolePolicy AssumeRolePolicy, roleName, irsaDomain, principal string) AssumeRolePolicy {
	desiredStatement := Statement{
		Effect: "Allow",
		Principal: Principal{
			Federated: principal,
		},
		Action: "sts:AssumeRoleWithWebIdentity",
		Condition: Condition{
			StringLike: map[string]string{
				fmt.Sprintf("%s:sub", irsaDomain): fmt.Sprintf("system:serviceaccount:*:%s", getServiceAccountName(roleName)),
			},
		},
	}

	for i, statement := range assumeRolePolicy.Statement {
		if statement.Principal.Federated == principal {
			assumeRolePolicy.Statement[i] = desiredStatement
			return assumeRolePolicy
		}
	}

	assumeRolePolicy.Statement = append(assumeRolePolicy.Statement, desiredStatement)

	return assumeRolePolicy
}

func (r *POCReconciler) removeStatement(assumeRolePolicy AssumeRolePolicy, roleName, principal string) AssumeRolePolicy {
	statements := []Statement{}
	for _, statement := range assumeRolePolicy.Statement {
		if statement.Principal.Federated != principal {
			statements = append(statements, statement)
		}
	}

	assumeRolePolicy.Statement = statements
	return assumeRolePolicy
}

func getServiceAccountName(roleName string) string {
	switch roleName {
	case "ALBController-Role":
		return "aws-load-balancer-controller"
	case "ebs-csi-driver-role":
		return "ebs-csi-controller-sa"
	case "efs-csi-driver-role":
		return "efs-csi-controller-sa"
	case "cluster-autoscaler-role":
		return "cluster-autoscaler"
	case "CertManager-Role":
		return "cert-manager"
	case "Route53Manager-Role":
		return "external-dns"
	default:
		return ""
	}
}

func (r POCReconciler) newIAMCLient(roleARN, region string) (*iam.IAM, error) {
	session, err := awssession.NewSession(&aws.Config{
		Region: aws.String(region),
	})
	if err != nil {
		return nil, microerror.Mask(err)
	}
	awsClientConfig := &aws.Config{Credentials: stscreds.NewCredentials(session, roleARN)}

	session, err = awssession.NewSession(awsClientConfig)
	if err != nil {
		return nil, microerror.Mask(err)
	}

	return iam.New(session, &aws.Config{Region: aws.String(region)}), nil
}
