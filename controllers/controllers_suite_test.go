package controllers_test

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubectl/pkg/scheme"
	capa "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	expcapa "sigs.k8s.io/cluster-api-provider-aws/exp/api/v1beta1"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
)

var _ = BeforeSuite(func() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	if value := os.Getenv("KUBEBUILDER_ASSETS"); value == "" {
		Skip("KUBEBUILDER_ASSETS environment variable missing")
	}

	By("bootstrapping envtest")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			// Versions must match `go.mod`
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "sigs.k8s.io", "cluster-api@v1.3.1", "config", "crd", "bases"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "sigs.k8s.io", "cluster-api-provider-aws@v1.5.2", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = capa.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = expcapa.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = capi.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down envtest")
	if testEnv == nil {
		return
	}
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func SetupNamespaceBeforeAfterEach(namespace *string) {
	BeforeEach(func() {
		*namespace = fmt.Sprintf("capa-iam-operator-test-%s", uuid.New())
		namespaceObj := &corev1.Namespace{}
		namespaceObj.Name = *namespace
		Expect(k8sClient.Create(context.Background(), namespaceObj)).To(Succeed())
	})
	AfterEach(func() {
		namespaceObj := &corev1.Namespace{}
		namespaceObj.Name = *namespace
		Expect(k8sClient.Delete(context.Background(), namespaceObj)).To(Succeed())
	})
}
