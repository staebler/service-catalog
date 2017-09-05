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
	"errors"
	"fmt"
	"io"

	"github.com/golang/glog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apiserver/pkg/admission"

	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog"
	scadmission "github.com/kubernetes-incubator/service-catalog/pkg/apiserver/admission"
	informers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/internalversion"
	internalversion "github.com/kubernetes-incubator/service-catalog/pkg/client/listers_generated/servicecatalog/internalversion"
)

const (
	// PluginName is name of admission plug-in
	PluginName = "BlockConcurrentUpdates"
)

// Register registers a plugin
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(io.Reader) (admission.Interface, error) {
		return NewBlockConcurrentUpdates(), nil
	})
}

// blockConcurrentUpdates is an implementation of admission.Interface.
// It blocks concurrent updates to ServiceInstance and ServiceInstanceCredential
// resources.
type blockConcurrentUpdates struct {
	*admission.Handler
	instanceLister           internalversion.ServiceInstanceLister
	instanceCredentialLister internalversion.ServiceInstanceCredentialLister
}

var _ = scadmission.WantsInternalServiceCatalogInformerFactory(&blockConcurrentUpdates{})

func (cu *blockConcurrentUpdates) Admit(a admission.Attributes) error {
	// we need to wait for our caches to warm
	if !cu.WaitForReady() {
		return admission.NewForbidden(a, fmt.Errorf("not yet ready to handle request"))
	}

	if a.GetResource().Group != servicecatalog.GroupName {
		return nil
	}

	// Do not block updates to the status of the resource as those updates need
	// to succeed during reconciliation
	if a.GetSubresource() == "status" {
		return nil
	}

	if a.GetResource().GroupResource() == servicecatalog.Resource("serviceinstances") {
		instance, ok := a.GetObject().(*servicecatalog.ServiceInstance)
		if !ok {
			return apierrors.NewBadRequest("Resource was marked with kind ServiceInstance but was unable to be converted")
		}
		if instance.DeletionTimestamp != nil || instance.Status.ReconciledGeneration != instance.Generation {
			msg := fmt.Sprintf("ServiceInstance %v/%v has a pending change already.", instance.Namespace, instance.Name)
			glog.V(4).Info(msg)
			return apierrors.NewConflict(a.GetResource().GroupResource(), fmt.Sprintf("%v/%v", instance.Namespace, instance.Name), errors.New("pending change"))
		}
	} else if a.GetResource().GroupResource() == servicecatalog.Resource("serviceinstancecredentials") {
		instanceCredential, ok := a.GetObject().(*servicecatalog.ServiceInstanceCredential)
		if !ok {
			return apierrors.NewBadRequest("Resource was marked with kind ServiceInstanceCredential but was unable to be converted")
		}
		if instanceCredential.Status.ReconciledGeneration != instanceCredential.Generation {
			msg := fmt.Sprintf("ServiceInstanceCredential %v/%v has a pending change already.", instanceCredential.Namespace, instanceCredential.Name)
			glog.V(4).Info(msg)
			return apierrors.NewConflict(a.GetResource().GroupResource(), fmt.Sprintf("%v/%v", instanceCredential.Namespace, instanceCredential.Name), errors.New("pending change"))
		}
	}
	return nil
}

// NewBlockConcurrentUpdates creates a new admission control handler that
// blocks concurrent updates to ServiceInstance and ServiceInstanceCredential
// resources.
func NewBlockConcurrentUpdates() admission.Interface {
	return &blockConcurrentUpdates{
		Handler: admission.NewHandler(admission.Update),
	}
}

func (cu *blockConcurrentUpdates) SetInternalServiceCatalogInformerFactory(f informers.SharedInformerFactory) {
	instanceInformer := f.Servicecatalog().InternalVersion().ServiceInstances()
	cu.instanceLister = instanceInformer.Lister()
	instanceCredentialInformer := f.Servicecatalog().InternalVersion().ServiceInstanceCredentials()
	cu.instanceCredentialLister = instanceCredentialInformer.Lister()
	cu.SetReadyFunc(func() bool {
		return instanceInformer.Informer().HasSynced() &&
			instanceCredentialInformer.Informer().HasSynced()
	})
}

func (cu *blockConcurrentUpdates) Validate() error {
	if cu.instanceLister == nil {
		return errors.New("missing service instance lister")
	}
	if cu.instanceCredentialLister == nil {
		return errors.New("missing service instance credential lister")
	}
	return nil
}
