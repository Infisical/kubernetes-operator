package auth_test

import (
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
)

func createSecret(name, namespace string, data map[string][]byte) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

func deleteSecret(name, namespace string) {
	secret := &corev1.Secret{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)).To(Succeed())
	Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
}

func newInfisicalAuth(method secretsv1beta1.InfisicalAuthMethod) *secretsv1beta1.InfisicalAuth {
	return &secretsv1beta1.InfisicalAuth{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-auth",
			Namespace: "default",
		},
		Spec: secretsv1beta1.InfisicalAuthSpec{
			Method: method,
		},
	}
}
