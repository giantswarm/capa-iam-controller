package iam

import (
	"bytes"
	"strings"
	"text/template"
)

const AWSReducedInstanceProfileIAMPermissionsForWorkersLabel = "alpha.aws.giantswarm.io/reduced-instance-permissions-workers"

func generatePolicyDocument(t string, params interface{}) (string, error) {
	tmpl, err := template.New("policy").Parse(t)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, params)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func ec2ServiceDomain(region string) string {
	domain := "ec2.amazonaws.com"

	if isChinaRegion(region) {
		domain += ".cn"
	}

	return domain
}

func awsDomain(region string) string {
	domain := "aws"

	if isChinaRegion(region) {
		domain = "aws-cn"
	}

	return domain
}

func isChinaRegion(region string) bool {
	return strings.Contains(region, "cn-")
}

func getInlinePolicyTemplate(roleType string, objectLabels map[string]string) string {
	switch roleType {
	case BastionRole:
		return bastionPolicyTemplate
	case ControlPlaneRole:
		return controlPlanePolicyTemplate
	case NodesRole:
		if labelValue := objectLabels[AWSReducedInstanceProfileIAMPermissionsForWorkersLabel]; labelValue == "true" {
			// Reduce permissions to zero. All applications on worker nodes that want to reach the AWS API must use
			// IRSA for credentials and must not fall back to the EC2 instance's IAM instance profile.
			return ""
		} else {
			// Previous default
			return nodesTemplate
		}
	case Route53Role:
		return route53RolePolicyTemplate
	case KIAMRole:
		return kiamRolePolicyTemplate
	case IRSARole:
		return route53RolePolicyTemplate
	case CertManagerRole:
		return route53RolePolicyTemplateForCertManager
	case ALBConrollerRole:
		return ALBControllerPolicyTemplate
	case EBSCSIDriverRole:
		return EBSCSIDriverPolicyTemplate
	case EFSCSIDriverRole:
		return EFSCSIDriverPolicyTemplate
	case ClusterAutoscalerRole:
		return clusterAutoscalerPolicyTemplate
	default:
		return ""
	}
}

func getTrustPolicyTemplate(roleType string) string {
	switch roleType {
	case BastionRole:
		return ec2TrustIdentityPolicyTemplate
	case ControlPlaneRole:
		return ec2TrustIdentityPolicyTemplate
	case NodesRole:
		return ec2TrustIdentityPolicyTemplate
	case Route53Role:
		return externalDnsTrustIdentityPolicyIRSA
	case KIAMRole:
		return kiamTrustIdentityPolicy
	case IRSARole:
		return trustIdentityPolicyIRSA
	case CertManagerRole:
		return trustIdentityPolicyIRSA
	case ALBConrollerRole:
		return albControllerTrustIdentityPolicyIRSA
	case EBSCSIDriverRole:
		return trustIdentityPolicyIRSA
	case EFSCSIDriverRole:
		return trustIdentityPolicyIRSA
	case ClusterAutoscalerRole:
		return trustIdentityPolicyIRSA

	default:
		return ""
	}
}
