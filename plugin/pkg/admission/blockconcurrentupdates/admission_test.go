/*
Copyright 2017 The Kubernetes Authors.

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

package blockconcurrentupdates

import (
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission"
	core "k8s.io/client-go/testing"

	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog"
	scadmission "github.com/kubernetes-incubator/service-catalog/pkg/apiserver/admission"
	"github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/internalclientset"
	"github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/internalclientset/fake"
	informers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/internalversion"
)

// newHandlerForTest returns a configured handler for testing.
func newHandlerForTest(internalClient internalclientset.Interface) (admission.Interface, informers.SharedInformerFactory, error) {
	f := informers.NewSharedInformerFactory(internalClient, 5*time.Minute)
	handler := NewBlockConcurrentUpdates()
	pluginInitializer := scadmission.NewPluginInitializer(internalClient, f, nil, nil)
	pluginInitializer.Initialize(handler)
	err := admission.Validate(handler)
	return handler, f, err
}

// newFakeServiceCatalogClientWithInstances creates a fake clientset that provides
// the specified ServiceInstance resources
func newFakeServiceCatalogClientWithInstances(instances ...servicecatalog.ServiceInstance) *fake.Clientset {
	fakeClient := &fake.Clientset{}

	instanceList := &servicecatalog.ServiceInstanceList{
		ListMeta: metav1.ListMeta{
			ResourceVersion: "1",
		},
	}
	instanceList.Items = append(instanceList.Items, instances...)

	fakeClient.AddReactor("list", "serviceinstances", func(action core.Action) (bool, runtime.Object, error) {
		return true, instanceList, nil
	})

	return fakeClient
}

// newFakeServiceCatalogClientWithInstanceCredentials creates a fake clientset
// that provides the specified ServiceInstanceCrendential resources
func newFakeServiceCatalogClientWithInstanceCredentials(instanceCredentials ...servicecatalog.ServiceInstanceCredential) *fake.Clientset {
	fakeClient := &fake.Clientset{}

	instanceCredentialList := &servicecatalog.ServiceInstanceCredentialList{
		ListMeta: metav1.ListMeta{
			ResourceVersion: "1",
		},
	}
	instanceCredentialList.Items = append(instanceCredentialList.Items, instanceCredentials...)

	fakeClient.AddReactor("list", "serviceinstancecredentials", func(action core.Action) (bool, runtime.Object, error) {
		return true, instanceCredentialList, nil
	})

	return fakeClient
}

func getTestServiceInstance() servicecatalog.ServiceInstance {
	return servicecatalog.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-instance",
			Namespace:  "test-namespace",
			Generation: 1,
		},
		Status: servicecatalog.ServiceInstanceStatus{
			ReconciledGeneration: 1,
		},
	}
}

func getTestServiceInstanceCredential() servicecatalog.ServiceInstanceCredential {
	return servicecatalog.ServiceInstanceCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-instance",
			Namespace:  "test-namespace",
			Generation: 1,
		},
		Status: servicecatalog.ServiceInstanceCredentialStatus{
			ReconciledGeneration: 1,
		},
	}
}

func TestAdmitInstance(t *testing.T) {
	cases := []struct {
		name              string
		oldInstance       servicecatalog.ServiceInstance
		newInstance       servicecatalog.ServiceInstance
		subresource       string
		expectedErrorCode int32
	}{
		{
			name:        "no pending change",
			oldInstance: getTestServiceInstance(),
			newInstance: getTestServiceInstance(),
		},
		{
			name:        "pending update",
			oldInstance: getTestServiceInstance(),
			newInstance: func() servicecatalog.ServiceInstance {
				i := getTestServiceInstance()
				i.Generation = 2
				return i
			}(),
			expectedErrorCode: 409,
		},
		{
			name:        "pending delete",
			oldInstance: getTestServiceInstance(),
			newInstance: func() servicecatalog.ServiceInstance {
				i := getTestServiceInstance()
				now := metav1.Now()
				i.DeletionTimestamp = &now
				return i
			}(),
			expectedErrorCode: 409,
		},
		{
			name:        "status update",
			oldInstance: getTestServiceInstance(),
			newInstance: func() servicecatalog.ServiceInstance {
				i := getTestServiceInstance()
				i.Generation = 2
				return i
			}(),
			subresource: "status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := newFakeServiceCatalogClientWithInstances(tc.oldInstance)
			handler, informerFactory, err := newHandlerForTest(fakeClient)
			if err != nil {
				t.Fatalf("unexpected error initializing handler: %v", err)
			}
			informerFactory.Start(wait.NeverStop)

			attr := admission.NewAttributesRecord(
				&tc.newInstance,
				nil,
				servicecatalog.Kind("ServiceInstance").WithVersion("version"),
				tc.newInstance.Namespace,
				tc.newInstance.Name,
				servicecatalog.Resource("serviceinstances").WithVersion("version"),
				tc.subresource,
				admission.Update,
				nil)
			err = handler.Admit(attr)
			if tc.expectedErrorCode == 0 {
				if err != nil {
					t.Fatalf("unexpected error from Admit: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error from Admit but got none")
				}
				statusError, ok := err.(*apierrors.StatusError)
				if !ok {
					t.Fatalf("unexpected type of error from Admit: expected %T, got %T", &apierrors.StatusError{}, err)
				}
				if e, a := tc.expectedErrorCode, statusError.Status().Code; e != a {
					t.Fatalf("unexpected status code in error from Admit: expected %v, got %v", e, a)
				}
			}
		})
	}
}

func TestAdmitInstanceCredential(t *testing.T) {
	cases := []struct {
		name                  string
		oldInstanceCredential servicecatalog.ServiceInstanceCredential
		newInstanceCredential servicecatalog.ServiceInstanceCredential
		subresource           string
		expectedErrorCode     int32
	}{
		{
			name: "no pending change",
			oldInstanceCredential: getTestServiceInstanceCredential(),
			newInstanceCredential: getTestServiceInstanceCredential(),
		},
		{
			name: "pending update",
			oldInstanceCredential: getTestServiceInstanceCredential(),
			newInstanceCredential: func() servicecatalog.ServiceInstanceCredential {
				ic := getTestServiceInstanceCredential()
				ic.Generation = 2
				return ic
			}(),
			expectedErrorCode: 409,
		},
		{
			name: "status update",
			oldInstanceCredential: getTestServiceInstanceCredential(),
			newInstanceCredential: func() servicecatalog.ServiceInstanceCredential {
				ic := getTestServiceInstanceCredential()
				ic.Generation = 2
				return ic
			}(),
			subresource: "status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := newFakeServiceCatalogClientWithInstanceCredentials(tc.oldInstanceCredential)
			handler, informerFactory, err := newHandlerForTest(fakeClient)
			if err != nil {
				t.Fatalf("unexpected error initializing handler: %v", err)
			}
			informerFactory.Start(wait.NeverStop)

			attr := admission.NewAttributesRecord(
				&tc.newInstanceCredential,
				nil,
				servicecatalog.Kind("ServiceInstanceCredential").WithVersion("version"),
				tc.newInstanceCredential.Namespace,
				tc.newInstanceCredential.Name, servicecatalog.Resource("serviceinstancecredentials").WithVersion("version"),
				tc.subresource,
				admission.Update,
				nil)
			err = handler.Admit(attr)
			if tc.expectedErrorCode == 0 {
				if err != nil {
					t.Fatalf("unexpected error from Admit: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error from Admit but got none")
				}
				statusError, ok := err.(*apierrors.StatusError)
				if !ok {
					t.Fatalf("unexpected type of error from Admit: expected %T, got %T", &apierrors.StatusError{}, err)
				}
				if e, a := tc.expectedErrorCode, statusError.Status().Code; e != a {
					t.Fatalf("unexpected status code in error from Admit: expected %v, got %v", e, a)
				}
			}
		})
	}
}
