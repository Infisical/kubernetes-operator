package conditions_test

import (
	"errors"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Infisical/infisical/k8-operator/internal/conditions"
)

var _ = Describe("SetReconcileStatus", func() {
	It("marks a successful reconciliation", func() {
		var list []metav1.Condition

		conditions.SetReconcileStatus(&list, 3, nil)

		condition := meta.FindStatusCondition(list, conditions.LastReconcileStatus)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal("OK"))
		Expect(condition.Message).To(Equal("Reconciliation successful"))
		Expect(condition.ObservedGeneration).To(BeEquivalentTo(3))
	})

	It("replaces an earlier outcome with the error", func() {
		var list []metav1.Condition
		conditions.SetReconcileStatus(&list, 1, nil)

		conditions.SetReconcileStatus(&list, 2, errors.New("boom"))

		Expect(list).To(HaveLen(1))
		Expect(list[0].Status).To(Equal(metav1.ConditionFalse))
		Expect(list[0].Reason).To(Equal("Error"))
		Expect(list[0].Message).To(Equal("Reconciliation failed: boom"))
		Expect(list[0].ObservedGeneration).To(BeEquivalentTo(2))
	})

	It("cuts a message the API server would reject to the bytes it accepts, keeping it valid UTF-8", func() {
		var list []metav1.Condition

		// "é" takes two bytes, so cutting at an exact byte count could split one.
		conditions.SetReconcileStatus(&list, 1, errors.New("x"+strings.Repeat("é", conditions.MaxMessageLength)))

		message := list[0].Message
		Expect(len(message)).To(BeNumerically("<=", conditions.MaxMessageLength))
		Expect(len(message)).To(BeNumerically(">", conditions.MaxMessageLength-4))
		Expect(utf8.ValidString(message)).To(BeTrue())
		Expect(message).To(HavePrefix("Reconciliation failed: xéé"))
		Expect(message).To(HaveSuffix("é... (truncated)"))
	})
})

var _ = Describe("SetAuthMethod", func() {
	It("records the method next to the other conditions", func() {
		var list []metav1.Condition
		conditions.SetReconcileStatus(&list, 1, nil)

		conditions.SetAuthMethod(&list, 1, "universal")

		Expect(list).To(HaveLen(2))
		condition := meta.FindStatusCondition(list, conditions.LastReconcileAuthMethod)
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Message).To(Equal("universal"))
	})
})
