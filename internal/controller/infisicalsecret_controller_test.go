/*
MIT License

Copyright (c) 2024 Infisical

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package controller

import (
	"testing"

	secretsv1alpha1 "github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestShouldReconcileInfisicalSecretUpdate(t *testing.T) {
	tests := []struct {
		name string
		old  metav1.ObjectMeta
		new  metav1.ObjectMeta
		want bool
	}{
		{
			name: "generation change",
			old:  metav1.ObjectMeta{Generation: 1},
			new:  metav1.ObjectMeta{Generation: 2},
			want: true,
		},
		{
			name: "force sync annotation change",
			old: metav1.ObjectMeta{
				Generation:  1,
				Annotations: map[string]string{"infisical.com/force-sync": "1"},
			},
			new: metav1.ObjectMeta{
				Generation:  1,
				Annotations: map[string]string{"infisical.com/force-sync": "2"},
			},
			want: true,
		},
		{
			name: "status only update",
			old: metav1.ObjectMeta{
				Generation:  1,
				Annotations: map[string]string{"infisical.com/force-sync": "1"},
			},
			new: metav1.ObjectMeta{
				Generation:  1,
				Annotations: map[string]string{"infisical.com/force-sync": "1"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := event.UpdateEvent{
				ObjectOld: &secretsv1alpha1.InfisicalSecret{ObjectMeta: tt.old},
				ObjectNew: &secretsv1alpha1.InfisicalSecret{ObjectMeta: tt.new},
			}

			if got := shouldReconcileInfisicalSecretUpdate(update); got != tt.want {
				t.Fatalf("shouldReconcileInfisicalSecretUpdate() = %t, want %t", got, tt.want)
			}
		})
	}
}
