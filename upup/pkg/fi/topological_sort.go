/*
Copyright 2019 The Kubernetes Authors.

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

package fi

import (
	"fmt"
	"reflect"

	"k8s.io/klog/v2"
	"k8s.io/kops/util/pkg/reflectutils"
)

type HasDependencies interface {
	GetDependencies(tasks map[string]Task) []Task
}

// NotADependency is a marker type to prevent FindTaskDependencies() from considering it a potential dependency.
type NotADependency struct{}

var _ HasDependencies = &NotADependency{}

func (NotADependency) GetDependencies(map[string]Task) []Task {
	return nil
}

// FindTaskDependencies returns a map from each task's key to the discovered list of dependencies
func FindTaskDependencies(tasks map[string]Task) map[string][]string {
	taskToId := make(map[interface{}]string)
	for k, t := range tasks {
		taskToId[t] = k
	}

	edges := make(map[string][]string)

	for k, t := range tasks {
		task := t

		var dependencies []Task
		if hd, ok := task.(HasDependencies); ok {
			dependencies = hd.GetDependencies(tasks)
		} else {
			dependencies = reflectForDependencies(tasks, task)
		}

		var dependencyKeys []string
		for _, dep := range dependencies {
			dependencyKey, found := taskToId[dep]
			if !found {
				klog.Fatalf("dependency not found: %v", dep)
			}
			dependencyKeys = append(dependencyKeys, dependencyKey)
		}

		edges[k] = dependencyKeys
	}

	klog.V(4).Infof("Dependencies:")
	for k, v := range edges {
		klog.V(4).Infof("\t%s:\t%v", k, v)
	}

	return edges
}

func reflectForDependencies(tasks map[string]Task, task Task) []Task {
	v := reflect.ValueOf(task).Elem()
	return getDependencies(tasks, v)
}

// FindDependencies will try to infer dependencies for an arbitrary object
func FindDependencies(tasks map[string]Task, o interface{}) []Task {
	if hd, ok := o.(HasDependencies); ok {
		return hd.GetDependencies(tasks)
	}

	v := reflect.ValueOf(o).Elem()
	return getDependencies(tasks, v)
}

func getDependencies(tasks map[string]Task, v reflect.Value) []Task {
	var dependencies []Task

	visitor := func(path *reflectutils.FieldPath, f *reflect.StructField, v reflect.Value) error {
		if reflectutils.IsPrimitiveValue(v) {
			return nil
		}

		switch v.Kind() {
		case reflect.String:
			return nil

		case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map:
			// The recursive walk will descend into this; we can ignore here
			return nil

		case reflect.Struct:
			if path.IsEmpty() {
				// Ignore self - we are a struct, but not our own dependency!
				return nil
			}

			// TODO: Can we / should we use a type-switch statement
			intf := v.Addr().Interface()
			if hd, ok := intf.(HasDependencies); ok {
				deps := hd.GetDependencies(tasks)
				dependencies = append(dependencies, deps...)
			} else if dep, ok := intf.(Task); ok {
				dependencies = append(dependencies, dep)
			} else if _, ok := intf.(Resource); ok {
				// Ignore: not a dependency, unless we explicitly implement HasDependencies (e.g. TaskDependentResource)
			} else {
				return fmt.Errorf("Unhandled type for %q: %T", path, v.Interface())
			}
			return reflectutils.SkipReflection

		default:
			klog.Infof("Unhandled kind for %q: %T", path, v.Interface())
			return fmt.Errorf("Unhandled kind for %q: %v", path, v.Kind())
		}
	}

	err := reflectutils.ReflectRecursive(v, visitor, &reflectutils.ReflectOptions{DeprecatedDoubleVisit: true})
	if err != nil {
		klog.Fatalf("unexpected error finding dependencies %v", err)
	}

	return dependencies
}
