package iam

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	awsclientgo "github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/eks/eksiface"
	awsiam "github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/giantswarm/microerror"
	"github.com/go-logr/logr"
)

const (
	BastionRole           = "bastion"
	ControlPlaneRole      = "control-plane" // also used as part of finalizer name
	NodesRole             = "nodes"         // also used as part of finalizer name
	Route53Role           = "route53-role"
	KIAMRole              = "kiam-role"
	IRSARole              = "irsa-role"
	CertManagerRole       = "cert-manager-role"
	ALBConrollerRole      = "ALBController-Role"
	EBSCSIDriverRole      = "ebs-csi-driver-role"
	EFSCSIDriverRole      = "efs-csi-driver-role"
	ClusterAutoscalerRole = "cluster-autoscaler-role"

	IAMControllerOwnedTag = "capi-iam-controller/owned"
	ClusterIDTag          = "sigs.k8s.io/cluster-api-provider-aws/cluster/%s"
)

type IAMServiceConfig struct {
	ObjectLabels     map[string]string // not always filled
	AWSSession       awsclientgo.ConfigProvider
	ClusterName      string
	MainRoleName     string
	Log              logr.Logger
	RoleType         string
	Region           string
	PrincipalRoleARN string
	CustomTags       map[string]string

	IAMClientFactory func(awsclientgo.ConfigProvider, string) iamiface.IAMAPI
}

type IAMService struct {
	objectLabels     map[string]string // not always filled
	clusterName      string
	iamClient        iamiface.IAMAPI
	eksClient        eksiface.EKSAPI
	mainRoleName     string
	log              logr.Logger
	region           string
	roleType         string
	principalRoleARN string
	customTags       map[string]string
}

type Route53RoleParams struct {
	AWSDomain        string
	EC2ServiceDomain string
	AccountID        string
	IRSATrustDomains []string
	Namespace        string
	ServiceAccount   string
	PrincipalRoleARN string
}

func New(config IAMServiceConfig) (*IAMService, error) {
	if config.AWSSession == nil {
		return nil, errors.New("cannot create IAMService with AWSSession equal to nil")
	}
	if config.IAMClientFactory == nil {
		return nil, errors.New("cannot create IAMService with IAMClientFactory equal to nil")
	}
	if config.ClusterName == "" {
		return nil, errors.New("cannot create IAMService with empty ClusterName")
	}
	if config.MainRoleName == "" {
		return nil, errors.New("cannot create IAMService with empty MainRoleName")
	}
	if !(config.RoleType == ControlPlaneRole || config.RoleType == NodesRole || config.RoleType == BastionRole || config.RoleType == IRSARole) {
		return nil, fmt.Errorf("cannot create IAMService with invalid RoleType '%s'", config.RoleType)
	}
	iamClient := config.IAMClientFactory(config.AWSSession, config.Region)
	eksClient := eks.New(config.AWSSession, &aws.Config{Region: aws.String(config.Region)})

	l := config.Log.WithValues("clusterName", config.ClusterName, "iam-role", config.RoleType)
	s := &IAMService{
		objectLabels:     config.ObjectLabels,
		clusterName:      config.ClusterName,
		iamClient:        iamClient,
		eksClient:        eksClient,
		mainRoleName:     config.MainRoleName,
		log:              l,
		roleType:         config.RoleType,
		region:           config.Region,
		principalRoleARN: config.PrincipalRoleARN,
		customTags:       config.CustomTags,
	}

	return s, nil
}

func (s *IAMService) ReconcileRole() error {
	s.log.Info("reconciling IAM role")

	params := struct {
		ClusterName      string
		EC2ServiceDomain string
	}{
		ClusterName:      s.clusterName,
		EC2ServiceDomain: ec2ServiceDomain(s.region),
	}
	err := s.reconcileRole(s.mainRoleName, s.roleType, params)
	if err != nil {
		return err
	}

	s.log.Info("finished reconciling IAM role")
	return nil
}

func (s *IAMService) ReconcileKiamRole() error {
	s.log.Info("reconciling KIAM IAM role")

	var controlPlaneRoleARN string
	{

		i := &awsiam.GetRoleInput{
			RoleName: aws.String(s.mainRoleName),
		}

		o, err := s.iamClient.GetRole(i)
		if err != nil {
			s.log.Error(err, "failed to fetch ControlPlane role")
			return err
		}

		controlPlaneRoleARN = *o.Role.Arn
	}

	params := struct {
		AWSDomain           string
		ControlPlaneRoleARN string
		EC2ServiceDomain    string
	}{
		AWSDomain:           awsDomain(s.region),
		ControlPlaneRoleARN: controlPlaneRoleARN,
		EC2ServiceDomain:    ec2ServiceDomain(s.region),
	}

	err := s.reconcileRole(roleName(KIAMRole, s.clusterName), KIAMRole, params)
	if err != nil {
		return err
	}

	s.log.Info("finished reconciling KIAM IAM role")
	return nil
}

func (s *IAMService) ReconcileRolesForIRSA(awsAccountID string, irsaTrustDomains []string) error {
	s.log.Info("reconciling IAM roles for IRSA")

	for _, roleTypeToReconcile := range getIRSARoles() {
		var params Route53RoleParams
		params, err := s.generateRoute53RoleParams(roleTypeToReconcile, awsAccountID, irsaTrustDomains)
		if err != nil {
			s.log.Error(err, "failed to generate Route53 role parameters")
			return err
		}

		err = s.reconcileRole(roleName(roleTypeToReconcile, s.clusterName), roleTypeToReconcile, params)
		if err != nil {
			return err
		}
	}

	s.log.Info("finished reconciling IAM roles for IRSA")
	return nil
}

func (s *IAMService) generateRoute53RoleParams(roleTypeToReconcile string, awsAccountID string, irsaTrustDomains []string) (Route53RoleParams, error) {
	if len(irsaTrustDomains) == 0 || slices.ContainsFunc(irsaTrustDomains, func(irsaTrustDomain string) bool { return irsaTrustDomain == "" }) {
		return Route53RoleParams{}, fmt.Errorf("irsaTrustDomains cannot be empty or have empty values: %v", irsaTrustDomains)
	}

	namespace := "kube-system"
	serviceAccount, err := getServiceAccount(roleTypeToReconcile)
	if err != nil {
		s.log.Error(err, "failed to get service account for role")
		return Route53RoleParams{}, err
	}

	params := Route53RoleParams{
		AWSDomain:        awsDomain(s.region),
		EC2ServiceDomain: ec2ServiceDomain(s.region),
		AccountID:        awsAccountID,
		IRSATrustDomains: irsaTrustDomains,
		Namespace:        namespace,
		ServiceAccount:   serviceAccount,
	}

	return params, nil
}

func (s *IAMService) reconcileRole(roleName string, roleType string, params interface{}) error {
	l := s.log.WithValues("role_name", roleName, "role_type", roleType)
	err := s.createRole(roleName, roleType, params)
	if err != nil {
		return err
	}

	if roleType == IRSARole || roleType == CertManagerRole || roleType == Route53Role || roleType == ALBConrollerRole || roleType == EBSCSIDriverRole || roleType == EFSCSIDriverRole || roleType == ClusterAutoscalerRole {
		if err = s.applyAssumePolicyRole(roleName, roleType, params); err != nil {
			l.Error(err, "Failed to apply assume role policy to role")
			return err
		}
	}

	// we only attach the inline policy to a role that is owned (and was created) by iam controller
	err = s.attachInlinePolicy(roleName, roleType, params)
	if err != nil {
		return err
	}

	return nil
}

// createRole will create requested IAM role
func (s *IAMService) createRole(roleName string, roleType string, params interface{}) error {
	l := s.log.WithValues("role_name", roleName, "role_type", roleType)

	_, err := s.iamClient.GetRole(&awsiam.GetRoleInput{
		RoleName: aws.String(roleName),
	})

	// create new IAMRole if it does not exist yet
	if err == nil {
		l.Info("IAM Role already exists, skipping creation")
		return nil
	}
	if !IsNotFound(err) {
		l.Error(err, "Failed to fetch IAM Role")
		return err
	}

	tmpl := getTrustPolicyTemplate(roleType)

	assumeRolePolicyDocument, err := generatePolicyDocument(tmpl, params)
	if err != nil {
		l.Error(err, "failed to generate assume policy document from template for IAM role")
		return err
	}

	tags := []*awsiam.Tag{
		{
			Key:   aws.String(IAMControllerOwnedTag),
			Value: aws.String(""),
		},
		{
			Key:   aws.String(fmt.Sprintf(ClusterIDTag, s.clusterName)),
			Value: aws.String("owned"),
		},
	}
	for k, v := range s.customTags {
		tags = append(tags, &awsiam.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	_, err = s.iamClient.CreateRole(&awsiam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicyDocument),
		Tags:                     tags,
	})
	if err != nil {
		l.Error(err, "failed to create IAM Role")
		return err
	}

	i2 := &awsiam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(roleName),
		Tags:                tags,
	}

	_, err = s.iamClient.CreateInstanceProfile(i2)
	if IsAlreadyExists(err) {
		// fall thru
	} else if err != nil {
		l.Error(err, "failed to create instance profile")
		return err
	}

	i3 := &awsiam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(roleName),
		RoleName:            aws.String(roleName),
	}

	_, err = s.iamClient.AddRoleToInstanceProfile(i3)
	if IsAlreadyExists(err) {
		// fall thru
	} else if err != nil {
		l.Error(err, "failed to add role to instance profile")
		return err
	}

	l.Info("successfully created a new IAM role")

	return nil
}

func (s *IAMService) applyAssumePolicyRole(roleName string, roleType string, params interface{}) error {
	log := s.log.WithValues("role_name", roleName)
	i := &awsiam.GetRoleInput{
		RoleName: aws.String(roleName),
	}

	_, err := s.iamClient.GetRole(i)

	if IsNotFound(err) {
		log.Info("role doesn't exist. Skipping application of assume policy")
		return nil
	}

	if !IsNotFound(err) && err != nil {
		log.Error(err, "failed to fetch IAM role")
		return err
	}

	log.Info("applying assume policy role to role")

	tmpl := getTrustPolicyTemplate(roleType)
	assumeRolePolicyDocument, err := generatePolicyDocument(tmpl, params)
	if err != nil {
		log.Error(err, "failed to generate assume policy document from template for IAM role")
		return err
	}

	updateInput := &awsiam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyDocument: aws.String(assumeRolePolicyDocument),
	}

	_, err = s.iamClient.UpdateAssumeRolePolicy(updateInput)

	return err
}

// attachInlinePolicy  will attach inline policy to the main IAM role
func (s *IAMService) attachInlinePolicy(roleName string, roleType string, params interface{}) error {
	l := s.log.WithValues("role_name", roleName)
	tmpl := getInlinePolicyTemplate(roleType, s.objectLabels)

	// For `NodesRole`, we reduced the permissions to zero, so the policy should not exist anymore. That results
	// in `tmpl == ""`. In that case, ensure below that the policy is deleted.
	wantPolicy := (tmpl != "")

	policyDocument, err := generatePolicyDocument(tmpl, params)
	if err != nil {
		l.Error(err, "failed to generate inline policy document from template for IAM role")
		return err
	}

	// check if the inline policy already exists
	output, err := s.iamClient.GetRolePolicy(&awsiam.GetRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String(policyName(s.roleType, s.clusterName)),
	})
	if err != nil && !IsNotFound(err) {
		l.Error(err, "failed to fetch inline policy for IAM role")
		return err
	}

	if err == nil {
		// Policy already exists

		if wantPolicy {
			isEqual, err := areEqualPolicy(*output.PolicyDocument, policyDocument)
			if err != nil {
				l.Error(err, "failed to compare inline policy documents")
				return err
			}
			if isEqual {
				l.Info("inline policy for IAM role already exists, skipping")
				return nil
			}
		}

		_, err = s.iamClient.DeleteRolePolicy(&awsiam.DeleteRolePolicyInput{
			PolicyName: aws.String(policyName(s.roleType, s.clusterName)),
			RoleName:   aws.String(roleName),
		})
		if err != nil {
			l.Error(err, "failed to delete inline policy from IAM Role")
			return err
		}
	}

	if !wantPolicy {
		l.Info("Not using any inline policy (apply zero permissions)")
		return nil
	}

	i := &awsiam.PutRolePolicyInput{
		PolicyName:     aws.String(policyName(s.roleType, s.clusterName)),
		PolicyDocument: aws.String(policyDocument),
		RoleName:       aws.String(roleName),
	}

	_, err = s.iamClient.PutRolePolicy(i)
	if err != nil {
		l.Error(err, "failed to add inline policy to IAM Role")
		return err
	}
	l.Info("successfully added inline policy to IAM role")

	return nil
}

func (s *IAMService) DeleteRole() error {
	s.log.Info("deleting IAM resources")

	// delete main role
	err := s.deleteRole(s.mainRoleName)
	if err != nil {
		return err
	}

	s.log.Info("finished deleting IAM resources")
	return nil
}

func (s *IAMService) DeleteKiamRole() error {
	s.log.Info("deleting KIAM IAM resources")

	// delete kiam role
	err := s.deleteRole(roleName(KIAMRole, s.clusterName))
	if err != nil {
		return err
	}

	s.log.Info("finished deleting KIAM IAM resources")
	return nil
}

func (s *IAMService) DeleteRoute53Role() error {
	s.log.Info("deleting Route53 IAM resources")

	// delete route3 role
	err := s.deleteRole(roleName(Route53Role, s.clusterName))
	if err != nil {
		return err
	}

	s.log.Info("finished deleting Route53 IAM resources")
	return nil
}

func (s *IAMService) DeleteRolesForIRSA() error {
	s.log.Info("deleting IAM roles for IRSA")
	defer s.log.Info("finished deleting IAM roles for IRSA")

	for _, roleTypeToReconcile := range getIRSARoles() {
		err := s.deleteRole(roleName(roleTypeToReconcile, s.clusterName))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *IAMService) deleteRole(roleName string) error {
	l := s.log.WithValues("role_name", roleName)

	// clean any attached policies, otherwise deletion of role will not work
	err := s.cleanRolePolicies(roleName)
	if err != nil {
		return err
	}

	i := &awsiam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String(roleName),
		RoleName:            aws.String(roleName),
	}

	_, err = s.iamClient.RemoveRoleFromInstanceProfile(i)
	if err != nil && !IsNotFound(err) {
		l.Error(err, "failed to remove role from instance profile")
		return err
	}

	i2 := &awsiam.DeleteInstanceProfileInput{
		InstanceProfileName: aws.String(roleName),
	}

	_, err = s.iamClient.DeleteInstanceProfile(i2)
	if err != nil && !IsNotFound(err) {
		l.Error(err, "failed to delete instance profile")
		return err
	}

	// delete the role
	i3 := &awsiam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	}

	_, err = s.iamClient.DeleteRole(i3)
	if err != nil && !IsNotFound(err) {
		l.Error(err, "failed to delete role")
		return err
	}

	return nil
}

func (s *IAMService) cleanRolePolicies(roleName string) error {
	l := s.log.WithValues("role_name", roleName)

	err := s.cleanAttachedPolicies(roleName)
	if err != nil {
		l.Error(err, "failed to clean attached policies from IAM Role")
		return err
	}

	err = s.cleanInlinePolicies(roleName)
	if err != nil {
		l.Error(err, "failed to clean inline policies from IAM Role")
		return err
	}

	l.Info("cleaned attached and inline policies from IAM Role")
	return nil
}

func (s *IAMService) cleanAttachedPolicies(roleName string) error {
	l := s.log.WithValues("role_name", roleName)
	i := &awsiam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	}

	o, err := s.iamClient.ListAttachedRolePolicies(i)
	if IsNotFound(err) {
		l.Info("role not found")
		return nil
	}
	if err != nil {
		l.Error(err, "failed to list attached policies")
		return err
	}

	for _, p := range o.AttachedPolicies {
		l.Info(fmt.Sprintf("detaching policy %s", *p.PolicyName))

		i := &awsiam.DetachRolePolicyInput{
			PolicyArn: p.PolicyArn,
			RoleName:  aws.String(roleName),
		}

		_, err := s.iamClient.DetachRolePolicy(i)
		if err != nil {
			l.Error(err, fmt.Sprintf("failed to detach policy %s", *p.PolicyName))
			return err
		}

		l.Info(fmt.Sprintf("detached policy %s", *p.PolicyName))
	}
	return nil
}

func (s *IAMService) cleanInlinePolicies(roleName string) error {
	l := s.log.WithValues("role_name", roleName)
	i := &awsiam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	}

	o, err := s.iamClient.ListRolePolicies(i)
	if IsNotFound(err) {
		l.Info("role not found")
		return nil
	}
	if err != nil {
		l.Error(err, "failed to list inline policies")
		return err
	}

	for _, p := range o.PolicyNames {
		l.Info(fmt.Sprintf("deleting inline policy %s", *p))

		i := &awsiam.DeleteRolePolicyInput{
			RoleName:   aws.String(roleName),
			PolicyName: p,
		}

		_, err := s.iamClient.DeleteRolePolicy(i)
		if err != nil && !IsNotFound(err) {
			l.Error(err, fmt.Sprintf("failed to delete inline policy %s", *p))
			return err
		}
		l.Info(fmt.Sprintf("deleted inline policy %s", *p))
	}

	return nil
}

func (s *IAMService) GetRoleARN(roleName string) (string, error) {
	o, err := s.iamClient.GetRole(&awsiam.GetRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return "", microerror.Mask(err)
	}

	return *o.Role.Arn, nil
}

func (s *IAMService) SetPrincipalRoleARN(arn string) {
	s.principalRoleARN = arn
}

func (s *IAMService) GetIRSAOpenIDForEKS(clusterName string) (string, error) {
	i := &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	}
	cluster, err := s.eksClient.DescribeCluster(i)
	if err != nil {
		return "", microerror.Mask(err)
	}
	if cluster == nil || cluster.Cluster == nil || cluster.Cluster.Identity == nil || cluster.Cluster.Identity.Oidc == nil || cluster.Cluster.Identity.Oidc.Issuer == nil {
		return "", microerror.Maskf(invalidClusterError, "cluster %s does not have OIDC identity", clusterName)
	}

	id := strings.TrimPrefix(*cluster.Cluster.Identity.Oidc.Issuer, "https://")

	return id, nil
}

func roleName(role string, clusterID string) string {
	if role == Route53Role {
		return fmt.Sprintf("%s-Route53Manager-Role", clusterID)
	} else if role == KIAMRole {
		return fmt.Sprintf("%s-IAMManager-Role", clusterID)
	} else if role == CertManagerRole {
		return fmt.Sprintf("%s-CertManager-Role", clusterID)
	} else {
		return fmt.Sprintf("%s-%s", clusterID, role)
	}
}

func policyName(role string, clusterID string) string {
	return fmt.Sprintf("%s-%s-policy", role, clusterID)
}

func getServiceAccount(role string) (string, error) {
	if role == CertManagerRole {
		return "cert-manager-app", nil
	} else if role == IRSARole {
		return "external-dns", nil
	} else if role == Route53Role {
		return "external-dns", nil
	} else if role == ALBConrollerRole {
		return "aws-load-balancer-controller", nil
	} else if role == EBSCSIDriverRole {
		return "ebs-csi-controller-sa", nil
	} else if role == EFSCSIDriverRole {
		return "efs-csi-sa", nil
	} else if role == ClusterAutoscalerRole {
		return "cluster-autoscaler", nil
	}

	return "", fmt.Errorf("cannot get service account for specified role - %s", role)
}

func getIRSARoles() []string {
	return []string{
		Route53Role,
		CertManagerRole,
		ALBConrollerRole,
		EBSCSIDriverRole,
		EFSCSIDriverRole,
		ClusterAutoscalerRole,
	}
}

func areEqualPolicy(encodedPolicy, expectedPolicy string) (bool, error) {
	decodedPolicy, err := urlDecode(encodedPolicy)
	if err != nil {
		return false, err
	}
	return areEqualJSON(decodedPolicy, expectedPolicy)
}

func areEqualJSON(s1, s2 string) (bool, error) {
	var o1 interface{}
	var o2 interface{}

	var err error
	err = json.Unmarshal([]byte(s1), &o1)
	if err != nil {
		return false, err
	}
	err = json.Unmarshal([]byte(s2), &o2)
	if err != nil {
		return false, err
	}

	return reflect.DeepEqual(o1, o2), nil
}

func urlDecode(encodedValue string) (string, error) {
	decodedValue, err := url.QueryUnescape(encodedValue)
	if err != nil {
		return "", err
	}

	return decodedValue, nil
}
