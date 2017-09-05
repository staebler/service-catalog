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

package validation

import (
	"github.com/ghodss/yaml"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	sc "github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog"
)

// validateServiceInstanceCredentialName is the validation function for ServiceInstanceCredential names.
var validateServiceInstanceCredentialName = apivalidation.NameIsDNSSubdomain

var validServiceInstanceCredentialOperations = map[sc.ServiceInstanceCredentialOperation]bool{
	sc.ServiceInstanceCredentialOperation(""):   true,
	sc.ServiceInstanceCredentialOperationBind:   true,
	sc.ServiceInstanceCredentialOperationUnbind: true,
}

// ValidateServiceInstanceCredential validates a ServiceInstanceCredential and returns a list of errors.
func ValidateServiceInstanceCredential(binding *sc.ServiceInstanceCredential) field.ErrorList {
	return internalValidateServiceInstanceCredential(binding, true)
}

func internalValidateServiceInstanceCredential(binding *sc.ServiceInstanceCredential, create bool) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, apivalidation.ValidateObjectMeta(&binding.ObjectMeta, true, /*namespace*/
		validateServiceInstanceCredentialName,
		field.NewPath("metadata"))...)
	allErrs = append(allErrs, validateServiceInstanceCredentialSpec(&binding.Spec, field.NewPath("Spec"), create)...)
	allErrs = append(allErrs, validateServiceInstanceCredentialStatus(&binding.Status, field.NewPath("Status"), create)...)
	if binding.Status.ReconciledGeneration == binding.Generation {
		if binding.Status.CurrentOperation != "" {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("Status").Child("currentOperation"), "currentOperation must not be present when reconciledGeneration andgeneration are the same"))
		}
	}
	return allErrs
}

func validateServiceInstanceCredentialSpec(spec *sc.ServiceInstanceCredentialSpec, fldPath *field.Path, create bool) field.ErrorList {
	allErrs := field.ErrorList{}

	for _, msg := range validateServiceInstanceName(spec.ServiceInstanceRef.Name, false /* prefix */) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("instanceRef", "name"), spec.ServiceInstanceRef.Name, msg))
	}

	for _, msg := range apivalidation.NameIsDNSSubdomain(spec.SecretName, false /* prefix */) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("secretName"), spec.SecretName, msg))
	}

	return allErrs
}

func validateServiceInstanceCredentialStatus(status *sc.ServiceInstanceCredentialStatus, fldPath *field.Path, create bool) field.ErrorList {
	allErrs := field.ErrorList{}

	if !validServiceInstanceCredentialOperations[status.CurrentOperation] {
		validValues := make([]string, len(validServiceInstanceCredentialOperations))
		i := 0
		for operation := range validServiceInstanceCredentialOperations {
			validValues[i] = string(operation)
			i++
		}
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("currentOperation"), status.CurrentOperation, validValues))
	}

	switch status.CurrentOperation {
	case sc.ServiceInstanceCredentialOperationBind, sc.ServiceInstanceCredentialOperationUnbind, "":
		// Valid values
	default:
		allErrs = append(allErrs)
	}

	if status.CurrentOperation == "" {
		if status.OperationStartTime != nil {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("operationStartTime"), "operationStartTime must not be present when currentOperation is not present"))
		}
	} else {
		if status.OperationStartTime == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("operationStartTime"), "operationStartTime is required when currentOperation is present"))
		}
		// Do not allow the binding to be ready if there is an on-going operation
		for i, c := range status.Conditions {
			if c.Type == sc.ServiceInstanceCredentialConditionReady && c.Status == sc.ConditionTrue {
				allErrs = append(allErrs, field.Forbidden(fldPath.Child("Conditions").Index(i), "Can not set ServiceInstanceCredentialConditionReady to true when there is an operation in progress"))
			}
		}
	}

	if status.CurrentOperation == sc.ServiceInstanceCredentialOperationBind {
		if status.InProgressProperties == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("inProgressProperties"), `inProgressProperties is required when currentOperation is "Bind"`))
		}
	} else {
		if status.InProgressProperties != nil {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("inProgressProperties"), `inProgressProperties must not be present when currentOperation is not "Bind"`))
		}
	}

	if status.InProgressProperties != nil {
		allErrs = append(allErrs, validateServiceInstanceCredentialPropertiesState(status.InProgressProperties, fldPath.Child("inProgressProperties"), create)...)
	}

	if status.ExternalProperties != nil {
		allErrs = append(allErrs, validateServiceInstanceCredentialPropertiesState(status.ExternalProperties, fldPath.Child("externalProperties"), create)...)
	}

	return allErrs
}

func validateServiceInstanceCredentialPropertiesState(propertiesState *sc.ServiceInstanceCredentialPropertiesState, fldPath *field.Path, create bool) field.ErrorList {
	allErrs := field.ErrorList{}

	if propertiesState.Parameters == nil {
		if propertiesState.ParametersChecksum != "" {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("parametersChecksum"), "parametersChecksum must be empty when there are no parameters"))
		}
	} else {
		if len(propertiesState.Parameters.Raw) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("parameters").Child("raw"), "raw must not be empty"))
		} else {
			unmarshalled := make(map[string]interface{})
			if err := yaml.Unmarshal(propertiesState.Parameters.Raw, &unmarshalled); err != nil {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("parameters").Child("raw"), propertiesState.Parameters.Raw, "raw must be valid yaml"))
			}
		}
		if propertiesState.ParametersChecksum == "" {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("parametersChecksum"), "parametersChecksum must not be empty when there are parameters"))
		}
	}

	if propertiesState.ParametersChecksum != "" {
		if len(propertiesState.ParametersChecksum) != 64 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("parametersChecksum"), propertiesState.ParametersChecksum, "parametersChecksum must be exactly 64 digits"))
		}
		if !stringIsHexadecimal(propertiesState.ParametersChecksum) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("parametersChecksum"), propertiesState.ParametersChecksum, "parametersChecksum must be a hexadecimal number"))
		}
	}

	return allErrs
}

// ValidateServiceInstanceCredentialUpdate checks that when changing from an older binding to a newer binding is okay.
func ValidateServiceInstanceCredentialUpdate(new *sc.ServiceInstanceCredential, old *sc.ServiceInstanceCredential) field.ErrorList {
	return internalValidateServiceInstanceCredential(new, false)
}

// ValidateServiceInstanceCredentialStatusUpdate checks that when changing from an older binding to a newer binding is okay.
func ValidateServiceInstanceCredentialStatusUpdate(new *sc.ServiceInstanceCredential, old *sc.ServiceInstanceCredential) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, ValidateServiceInstanceCredentialUpdate(new, old)...)
	return allErrs
}
