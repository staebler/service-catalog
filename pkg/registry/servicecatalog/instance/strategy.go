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

package instance

// this was copied from where else and edited to fit our objects

import (
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage/names"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	api "k8s.io/client-go/pkg/api"

	"github.com/golang/glog"
	sc "github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog"
	scv "github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/validation"
	scfeatures "github.com/kubernetes-incubator/service-catalog/pkg/features"
)

// NewScopeStrategy returns a new NamespaceScopedStrategy for instances
func NewScopeStrategy() rest.NamespaceScopedStrategy {
	return instanceRESTStrategies
}

// implements interfaces RESTCreateStrategy, RESTUpdateStrategy, RESTDeleteStrategy,
// NamespaceScopedStrategy, and RESTGracefulDeleteStrategy.
// The implementation disallows any modifications to the instance.Status fields.
type instanceRESTStrategy struct {
	runtime.ObjectTyper // inherit ObjectKinds method
	names.NameGenerator // GenerateName method for CreateStrategy
}

// implements interface RESTUpdateStrategy. This implementation validates updates to
// instance.Status updates only and disallows any modifications to the instance.Spec.
type instanceStatusRESTStrategy struct {
	instanceRESTStrategy
}

var (
	instanceRESTStrategies = instanceRESTStrategy{
		// embeds to pull in existing code behavior from upstream

		ObjectTyper: api.Scheme,
		// use the generator from upstream k8s, or implement method
		// `GenerateName(base string) string`
		NameGenerator: names.SimpleNameGenerator,
	}
	_ rest.RESTCreateStrategy         = instanceRESTStrategies
	_ rest.RESTUpdateStrategy         = instanceRESTStrategies
	_ rest.RESTDeleteStrategy         = instanceRESTStrategies
	_ rest.RESTGracefulDeleteStrategy = instanceRESTStrategies

	instanceStatusUpdateStrategy = instanceStatusRESTStrategy{
		instanceRESTStrategies,
	}
	_ rest.RESTUpdateStrategy = instanceStatusUpdateStrategy
)

// Canonicalize does not transform a instance.
func (instanceRESTStrategy) Canonicalize(obj runtime.Object) {
	_, ok := obj.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to create")
	}
}

// NamespaceScoped returns false as instances are not scoped to a namespace.
func (instanceRESTStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForCreate receives a the incoming ServiceInstance and clears it's
// Status. Status is not a user settable field.
func (instanceRESTStrategy) PrepareForCreate(ctx genericapirequest.Context, obj runtime.Object) {
	instance, ok := obj.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to create")
	}

	if utilfeature.DefaultFeatureGate.Enabled(scfeatures.OriginatingIdentity) {
		setServiceInstanceUserInfo(instance, ctx)
	}

	// Creating a brand new object, thus it must have no
	// status. We can't fail here if they passed a status in, so
	// we just wipe it clean.
	instance.Status = sc.ServiceInstanceStatus{}
	// Fill in the first entry set to "creating"?
	instance.Status.Conditions = []sc.ServiceInstanceCondition{}
	instance.Finalizers = []string{sc.FinalizerServiceCatalog}
	instance.Generation = 1
}

func (instanceRESTStrategy) Validate(ctx genericapirequest.Context, obj runtime.Object) field.ErrorList {
	return scv.ValidateServiceInstance(obj.(*sc.ServiceInstance))
}

func (instanceRESTStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (instanceRESTStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (instanceRESTStrategy) PrepareForUpdate(ctx genericapirequest.Context, new, old runtime.Object) {
	newServiceInstance, ok := new.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to update to")
	}
	oldServiceInstance, ok := old.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to update from")
	}

	// Do not allow any updates to the Status field while updating the Spec
	newServiceInstance.Status = oldServiceInstance.Status

	// Spec updates bump the generation so that we can distinguish between
	// spec changes and other changes to the object.
	if !apiequality.Semantic.DeepEqual(oldServiceInstance.Spec, newServiceInstance.Spec) {
		if utilfeature.DefaultFeatureGate.Enabled(scfeatures.OriginatingIdentity) {
			setServiceInstanceUserInfo(newServiceInstance, ctx)
		}
		newServiceInstance.Generation = oldServiceInstance.Generation + 1
		setServiceInstanceReadyFalseCondition(newServiceInstance)
	}
}

func (instanceRESTStrategy) ValidateUpdate(ctx genericapirequest.Context, new, old runtime.Object) field.ErrorList {
	newServiceInstance, ok := new.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to validate to")
	}
	oldServiceInstance, ok := old.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to validate from")
	}

	return scv.ValidateServiceInstanceUpdate(newServiceInstance, oldServiceInstance)
}

// CheckGracefulDelete sets the UserInfo on the resource to that of the user that
// initiated the delete.
// Note that this is a hack way of setting the UserInfo. However, there is not
// currently any other mechanism in the Delete strategies for getting access to
// the resource being deleted and the context.
func (instanceRESTStrategy) CheckGracefulDelete(ctx genericapirequest.Context, obj runtime.Object, options *metav1.DeleteOptions) bool {
	if utilfeature.DefaultFeatureGate.Enabled(scfeatures.OriginatingIdentity) {
		serviceInstance, ok := obj.(*sc.ServiceInstance)
		if !ok {
			glog.Fatal("received a non-instance object to delete")
		}
		setServiceInstanceUserInfo(serviceInstance, ctx)
	}
	// Don't actually do graceful deletion. We are just using this strategy to set the user info prior to reconciling the delete.
	return false
}

func (instanceStatusRESTStrategy) PrepareForUpdate(ctx genericapirequest.Context, new, old runtime.Object) {
	newServiceInstance, ok := new.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to update to")
	}
	oldServiceInstance, ok := old.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to update from")
	}
	// Status changes are not allowed to update spec
	newServiceInstance.Spec = oldServiceInstance.Spec
}

func (instanceStatusRESTStrategy) ValidateUpdate(ctx genericapirequest.Context, new, old runtime.Object) field.ErrorList {
	newServiceInstance, ok := new.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to validate to")
	}
	oldServiceInstance, ok := old.(*sc.ServiceInstance)
	if !ok {
		glog.Fatal("received a non-instance object to validate from")
	}

	return scv.ValidateServiceInstanceStatusUpdate(newServiceInstance, oldServiceInstance)
}

// setServiceInstanceUserInfo injects user.Info from the request context
func setServiceInstanceUserInfo(instance *sc.ServiceInstance, ctx genericapirequest.Context) {
	instance.Spec.UserInfo = nil
	if user, ok := genericapirequest.UserFrom(ctx); ok {
		instance.Spec.UserInfo = &sc.UserInfo{
			Username: user.GetName(),
			UID:      user.GetUID(),
			Groups:   user.GetGroups(),
		}
		if extra := user.GetExtra(); len(extra) > 0 {
			instance.Spec.UserInfo.Extra = map[string]sc.ExtraValue{}
			for k, v := range extra {
				instance.Spec.UserInfo.Extra[k] = sc.ExtraValue(v)
			}
		}
	}
}

func setServiceInstanceReadyFalseCondition(instance *sc.ServiceInstance) {
	newCondition := sc.ServiceInstanceCondition{
		Type:    sc.ServiceInstanceConditionReady,
		Status:  sc.ConditionFalse,
		Reason:  "UpdateInitiated",
		Message: "Update initiated on ServiceInstance",
	}

	if len(instance.Status.Conditions) == 0 {
		newCondition.LastTransitionTime = metav1.Now()
		instance.Status.Conditions = []sc.ServiceInstanceCondition{newCondition}
		return
	}

	for i, cond := range instance.Status.Conditions {
		if cond.Type == sc.ServiceInstanceConditionReady {
			if cond.Status != newCondition.Status {
				newCondition.LastTransitionTime = metav1.Now()
			} else {
				newCondition.LastTransitionTime = cond.LastTransitionTime
			}

			instance.Status.Conditions[i] = newCondition
			return
		}
	}

	newCondition.LastTransitionTime = metav1.Now()
	instance.Status.Conditions = append(instance.Status.Conditions, newCondition)
}
