package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	managedAddonCatalogName = "addon-managed-odh-catalog"
	rhoaiOperatorPackage    = "rhods-operator"
)

const olmV1Group = "olm.operatorframework.io"

// OLMv1 GVKs use unstructured clients so this package does not import
// github.com/operator-framework/operator-controller/api.
//
//nolint:gochecknoglobals // Immutable GVK constants.
var (
	platformClusterCatalogGVK = schema.GroupVersionKind{
		Group: olmV1Group, Version: "v1", Kind: "ClusterCatalog",
	}
	platformClusterExtensionGVK = schema.GroupVersionKind{
		Group: olmV1Group, Version: "v1", Kind: "ClusterExtension",
	}
)

// clusterCatalogExists reports whether a cluster-scoped ClusterCatalog exists.
// IsNoMatchError and NotFound are treated as absent (false, nil).
func clusterCatalogExists(ctx context.Context, cli client.Reader, name string) (bool, error) {
	catalog := &unstructured.Unstructured{}
	catalog.SetGroupVersionKind(platformClusterCatalogGVK)

	err := cli.Get(ctx, client.ObjectKey{Name: name}, catalog)
	if err == nil {
		return true, nil
	}

	if meta.IsNoMatchError(err) {
		return false, nil
	}

	if client.IgnoreNotFound(err) != nil {
		return false, err
	}

	return false, nil
}

// ClusterExtensionInstallsPackage reports whether a ClusterExtension requests
// the given OLM package. Matching uses spec.source.catalog.packageName when
// spec.source.sourceType is "Catalog"; the ClusterExtension resource name is
// arbitrary. When installNamespace is non-empty, the extension must also
// target that namespace (spec.namespace).
//
// List errors are returned as-is, including [meta.IsNoMatchError] when the
// ClusterExtension CRD is absent. Callers decide how to handle API absence.
func ClusterExtensionInstallsPackage(
	ctx context.Context, cli client.Reader, packageName, installNamespace string,
) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(platformClusterExtensionGVK)

	err := cli.List(ctx, list)
	if err != nil {
		return false, err
	}

	for i := range list.Items {
		match, matchErr := clusterExtensionInstallsPackage(&list.Items[i], packageName, installNamespace)
		if matchErr != nil {
			return false, matchErr
		}

		if match {
			return true, nil
		}
	}

	return false, nil
}

func clusterExtensionInstallsPackage(
	ext *unstructured.Unstructured, packageName, installNamespace string,
) (bool, error) {
	if installNamespace != "" {
		ns, found, err := unstructured.NestedString(ext.Object, "spec", "namespace")
		if err != nil {
			return false, fmt.Errorf("read ClusterExtension spec.namespace: %w", err)
		}

		if !found || ns != installNamespace {
			return false, nil
		}
	}

	sourceType, found, err := unstructured.NestedString(ext.Object, "spec", "source", "sourceType")
	if err != nil {
		return false, fmt.Errorf("read ClusterExtension spec.source.sourceType: %w", err)
	}

	if !found || sourceType != "Catalog" {
		return false, nil
	}

	pkg, found, err := unstructured.NestedString(ext.Object, "spec", "source", "catalog", "packageName")
	if err != nil {
		return false, fmt.Errorf("read ClusterExtension spec.source.catalog.packageName: %w", err)
	}

	return found && pkg == packageName, nil
}
