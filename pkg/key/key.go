package key

import (
	"context"
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capa "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ClusterNameLabel = "cluster.x-k8s.io/cluster-name"
)

func FinalizerName(roleName string) string {
	return fmt.Sprintf("capa-iam-controller.finalizers.giantswarm.io/%s", roleName)
}

func GetClusterIDFromLabels(t v1.ObjectMeta) string {
	return t.GetLabels()[ClusterNameLabel]
}

func GetAWSClusterByName(ctx context.Context, ctrlClient client.Client, clusterName string) (*capa.AWSCluster, error) {
	awsClusterList := &capa.AWSClusterList{}

	if err := ctrlClient.List(ctx,
		awsClusterList,
		client.MatchingLabels{ClusterNameLabel: clusterName},
	); err != nil {
		return nil, err
	}

	if len(awsClusterList.Items) != 1 {
		return nil, fmt.Errorf("expected 1 AWSCluster but found %d", len(awsClusterList.Items))
	}

	return &awsClusterList.Items[0], nil
}
