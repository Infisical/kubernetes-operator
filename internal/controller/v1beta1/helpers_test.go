package v1beta1_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
)

type infisicalConnectionOpts struct {
	Name      string
	Namespace string
	Spec      secretsv1beta1.InfisicalConnectionSpec
}

type InfisicalConnectionOpt func(*infisicalConnectionOpts)

func WithName(name string) InfisicalConnectionOpt {
	return func(o *infisicalConnectionOpts) { o.Name = name }
}

func WithNamespace(namespace string) InfisicalConnectionOpt {
	return func(o *infisicalConnectionOpts) { o.Namespace = namespace }
}

func WithSpec(spec secretsv1beta1.InfisicalConnectionSpec) InfisicalConnectionOpt {
	return func(o *infisicalConnectionOpts) { o.Spec = spec }
}

func WithTLS(secretName, secretNamespace, secretKey string) InfisicalConnectionOpt {
	return func(o *infisicalConnectionOpts) {
		o.Spec.TLS = secretsv1beta1.TLSConfig{
			CaCertificate: secretsv1beta1.CaCertificate{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				SecretKey:       secretKey,
			},
		}
	}
}

func defaultInfisicalConnectionOpts() infisicalConnectionOpts {
	return infisicalConnectionOpts{
		Name:      "infisical-connection",
		Namespace: "default",
		Spec: secretsv1beta1.InfisicalConnectionSpec{
			Host: "https://app.infisical.com",
		},
	}
}

func createSelfSignedCACertSecret(ctx context.Context, name, namespace, key string) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			key: certPEM,
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

func deleteSecret(ctx context.Context, name, namespace string) {
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
}

func createInfisicalConnection(ctx context.Context, opts ...InfisicalConnectionOpt) *secretsv1beta1.InfisicalConnection {
	o := defaultInfisicalConnectionOpts()
	for _, fn := range opts {
		fn(&o)
	}

	resource := &secretsv1beta1.InfisicalConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
		},
		Spec: o.Spec,
	}
	Expect(k8sClient.Create(ctx, resource)).To(Succeed())
	return resource
}

func deleteInfisicalConnection(ctx context.Context, opts ...InfisicalConnectionOpt) {
	o := defaultInfisicalConnectionOpts()
	for _, fn := range opts {
		fn(&o)
	}

	resource := &secretsv1beta1.InfisicalConnection{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: o.Name, Namespace: o.Namespace}, resource)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
}
