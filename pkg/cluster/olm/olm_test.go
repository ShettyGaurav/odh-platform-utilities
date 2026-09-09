package olm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-platform-utilities/pkg/cluster/olm"
)

var (
	errAPIFailure          = errors.New("api failure")
	errSubscriptionAPI     = errors.New("subscription API failure")
	errClusterExtensionAPI = errors.New("cluster extension API failure")
)

type erroringOLMClient struct {
	client.Reader

	listErr  error
	listErrs map[schema.GroupVersionKind]error
	getErr   error
}

func (c *erroringOLMClient) Get(
	ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption,
) error {
	if c.getErr != nil {
		return c.getErr
	}

	return c.Reader.Get(ctx, key, obj, opts...)
}

func (c *erroringOLMClient) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if c.listErr != nil {
		return c.listErr
	}

	err := c.listErrs[list.GetObjectKind().GroupVersionKind()]
	if err != nil {
		return err
	}

	return c.Reader.List(ctx, list, opts...)
}

func TestOperatorPackageRequested_APIErrors(t *testing.T) { //nolint:funlen // Error combinations are table-driven.
	t.Parallel()

	subscriptionGVK := schema.GroupVersionKind{
		Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription",
	}
	clusterExtensionGVK := schema.GroupVersionKind{
		Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension",
	}
	subscriptionNoMatch := &meta.NoKindMatchError{
		GroupKind: subscriptionGVK.GroupKind(), SearchedVersions: []string{subscriptionGVK.Version},
	}
	clusterExtensionNoMatch := &meta.NoKindMatchError{
		GroupKind:        clusterExtensionGVK.GroupKind(),
		SearchedVersions: []string{clusterExtensionGVK.Version},
	}
	tests := []struct {
		name        string
		wantErr     error
		listErrs    map[schema.GroupVersionKind]error
		objects     []client.Object
		want        bool
		wantNoMatch bool
	}{
		{
			name:    "OLMv0 unavailable and OLMv1 dependency found",
			objects: []client.Object{newClusterExtension("my-operator", "my-operator")},
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK: subscriptionNoMatch,
			},
			want: true,
		},
		{
			name: "OLMv0 unavailable and dependency absent from OLMv1",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK: subscriptionNoMatch,
			},
		},
		{
			name: "OLMv1 unavailable and dependency absent from OLMv0",
			listErrs: map[schema.GroupVersionKind]error{
				clusterExtensionGVK: clusterExtensionNoMatch,
			},
		},
		{
			name: "both OLM APIs unavailable",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK:     subscriptionNoMatch,
				clusterExtensionGVK: clusterExtensionNoMatch,
			},
			wantNoMatch: true,
		},
		{
			name: "OLMv0 API failure",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK: errSubscriptionAPI,
			},
			wantErr: errSubscriptionAPI,
		},
		{
			name: "OLMv1 API failure",
			listErrs: map[schema.GroupVersionKind]error{
				clusterExtensionGVK: errClusterExtensionAPI,
			},
			wantErr: errClusterExtensionAPI,
		},
		{
			name: "OLMv0 unavailable and OLMv1 API failure",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK:     subscriptionNoMatch,
				clusterExtensionGVK: errClusterExtensionAPI,
			},
			wantErr: errClusterExtensionAPI,
		},
		{
			name: "OLMv0 API failure and OLMv1 unavailable",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK:     errSubscriptionAPI,
				clusterExtensionGVK: clusterExtensionNoMatch,
			},
			wantErr: errSubscriptionAPI,
		},
		{
			name:    "OLMv1 match wins despite OLMv0 API failure",
			objects: []client.Object{newClusterExtension("my-operator", "my-operator")},
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK: errSubscriptionAPI,
			},
			want: true,
		},
		{
			name: "OLMv0 error takes precedence when both APIs fail",
			listErrs: map[schema.GroupVersionKind]error{
				subscriptionGVK:     errSubscriptionAPI,
				clusterExtensionGVK: errClusterExtensionAPI,
			},
			wantErr: errSubscriptionAPI,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			baseCli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()
			cli := &erroringOLMClient{
				Reader:   baseCli,
				listErrs: tc.listErrs,
			}

			exists, err := olm.OperatorPackageRequested(t.Context(), cli, "my-operator")
			if tc.wantNoMatch {
				require.True(t, meta.IsNoMatchError(err), "expected no-match error, got %v", err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}

			assert.Equal(t, tc.want, exists)
		})
	}
}

func TestOperatorExists(t *testing.T) { //nolint:funlen // Table-driven test with many cases.
	t.Parallel()

	tests := []struct {
		name     string
		prefix   string
		wantVer  string
		objects  []client.Object
		wantInfo bool
	}{
		{
			name:   "operator found with version",
			prefix: "rhods-operator",
			objects: []client.Object{
				newOperatorCondition("rhods-operator.v1.2.3"),
			},
			wantInfo: true,
			wantVer:  "v1.2.3",
		},
		{
			name:   "operator found without v prefix",
			prefix: "rhods-operator",
			objects: []client.Object{
				newOperatorCondition("rhods-operator.1.2.3"),
			},
			wantInfo: true,
			wantVer:  "v1.2.3",
		},
		{
			name:   "operator found with empty version",
			prefix: "rhods-operator",
			objects: []client.Object{
				newOperatorCondition("rhods-operator."),
			},
			wantInfo: true,
			wantVer:  "",
		},
		{
			name:     "operator not found",
			prefix:   "rhods-operator",
			objects:  nil,
			wantInfo: false,
		},
		{
			name:   "different operator present, target not found",
			prefix: "rhods-operator",
			objects: []client.Object{
				newOperatorCondition("other-operator.v1.0.0"),
			},
			wantInfo: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()

			info, err := olm.OperatorExists(t.Context(), cli, tc.prefix)

			if !tc.wantInfo {
				require.ErrorIs(t, err, olm.ErrOperatorNotInstalled)
				assert.Nil(t, info)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, tc.wantVer, info.Version)
		})
	}
}

func TestOperatorPackageRequested(t *testing.T) { //nolint:funlen // Shared table covers both OLM APIs.
	t.Parallel()

	type testCase struct {
		name        string
		packageName string
		wantErr     string
		objects     []client.Object
		want        bool
	}

	tests := make([]testCase, 0, 11)
	tests = append(tests,
		testCase{name: "no requests", packageName: "my-operator"},
		testCase{name: "different packages in both APIs", packageName: "my-operator", objects: []client.Object{
			newSubscription("my-operator", "other-package"),
			newClusterExtension("my-operator", "another-package"),
		}},
		testCase{name: "OLMv1 match after unrelated Subscription", packageName: "my-operator", objects: []client.Object{
			newSubscription("my-operator", "other-package"),
			newClusterExtension("custom-extension", "my-operator"),
		}, want: true},
	)

	for _, api := range []struct {
		name        string
		newObject   func(string, string) *unstructured.Unstructured
		packagePath []string
	}{
		{name: "OLMv0", newObject: newSubscription, packagePath: []string{"spec", "name"}},
		{name: "OLMv1", newObject: newClusterExtension,
			packagePath: []string{"spec", "source", "catalog", "packageName"}},
	} {
		missing := api.newObject("custom-resource", "operator-package")
		unstructured.RemoveNestedField(missing.Object, api.packagePath...)
		malformed := api.newObject("custom-resource", "operator-package")
		require.NoError(t, unstructured.SetNestedField(malformed.Object, int64(42), api.packagePath...))

		tests = append(tests,
			testCase{name: api.name + "/package matches despite different resource name", packageName: "operator-package",
				objects: []client.Object{api.newObject("custom-resource", "operator-package")}, want: true},
			testCase{name: api.name + "/resource name is not package identity", packageName: "custom-resource",
				objects: []client.Object{api.newObject("custom-resource", "operator-package")}},
			testCase{name: api.name + "/missing package field", packageName: "operator-package",
				objects: []client.Object{missing}},
			testCase{name: api.name + "/malformed package field", packageName: "operator-package",
				objects: []client.Object{malformed}, wantErr: "read " + strings.Join(api.packagePath, ".")},
		)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()

			requested, err := olm.OperatorPackageRequested(t.Context(), cli, tc.packageName)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, requested)
		})
	}
}

func TestGetSubscription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		subName   string
		objects   []client.Object
		wantErr   bool
	}{
		{
			name:      "subscription found",
			namespace: "operators",
			subName:   "my-operator",
			objects: []client.Object{
				newSubscription("my-operator", "my-operator"),
			},
		},
		{
			name:      "subscription not found",
			namespace: "operators",
			subName:   "missing",
			objects:   nil,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()

			sub, err := olm.GetSubscription(t.Context(), cli, tc.namespace, tc.subName)
			if tc.wantErr {
				require.True(t, apierrors.IsNotFound(err), "expected not-found error, got %v", err)
				assert.Nil(t, sub)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, sub)
			assert.Equal(t, tc.subName, sub.GetName())
		})
	}
}

func TestCatalogSourceExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csName    string
		namespace string
		objects   []client.Object
		want      bool
	}{
		{
			name:      "catalog source found",
			csName:    "addon-managed-odh-catalog",
			namespace: "redhat-ods-operator",
			objects: []client.Object{
				newCatalogSource("addon-managed-odh-catalog", "redhat-ods-operator"),
			},
			want: true,
		},
		{
			name:      "catalog source not found",
			csName:    "addon-managed-odh-catalog",
			namespace: "redhat-ods-operator",
			objects:   nil,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()

			result, err := olm.CatalogSourceExists(t.Context(), cli, tc.namespace, tc.csName)
			require.NoError(t, err)
			assert.Equal(t, tc.want, result)
		})
	}
}

// --- helpers ---

func newOperatorCondition(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v2",
			"kind":       "OperatorCondition",
			"metadata":   map[string]any{"name": name},
		},
	}
}

func newSubscription(name, packageName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"spec":       map[string]any{"name": packageName},
			"metadata": map[string]any{
				"name":      name,
				"namespace": "operators",
			},
		},
	}
}

func newClusterExtension(name, packageName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtension",
			"spec": map[string]any{"source": map[string]any{
				"sourceType": "Catalog",
				"catalog":    map[string]any{"packageName": packageName},
			}},
			"metadata": map[string]any{"name": name},
		},
	}
}

func newCatalogSource(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "CatalogSource",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
}

func TestOperatorExists_APIError(t *testing.T) {
	t.Parallel()

	baseCli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	cli := &erroringOLMClient{Reader: baseCli, listErr: errAPIFailure}

	_, err := olm.OperatorExists(t.Context(), cli, "rhods-operator")
	require.ErrorIs(t, err, errAPIFailure)
}

func TestGetSubscription_APIError(t *testing.T) {
	t.Parallel()

	baseCli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	cli := &erroringOLMClient{Reader: baseCli, getErr: errAPIFailure}

	_, err := olm.GetSubscription(t.Context(), cli, "operators", "my-operator")
	require.ErrorIs(t, err, errAPIFailure)
}

func TestCatalogSourceExists_APIError(t *testing.T) {
	t.Parallel()

	baseCli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	cli := &erroringOLMClient{Reader: baseCli, getErr: errAPIFailure}

	_, err := olm.CatalogSourceExists(t.Context(), cli, "redhat-ods-operator", "addon-managed-odh-catalog")
	require.ErrorIs(t, err, errAPIFailure)
}

func TestSubscriptionExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		listErr error
		name    string
		objects []client.Object
		want    bool
	}{
		{name: "OLMv0", objects: []client.Object{newSubscription("my-operator", "different-package")}, want: true},
		{name: "same-name ClusterExtension is not a Subscription",
			objects: []client.Object{newClusterExtension("my-operator", "my-operator")}},
		{name: "absent"},
		{name: "package name is not resource name",
			objects: []client.Object{newSubscription("custom-subscription", "my-operator")}},
		{name: "API failure", listErr: errAPIFailure},
		{name: "Subscription API unavailable", listErr: &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: "operators.coreos.com", Kind: "Subscription"},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			baseCli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()
			cli := &erroringOLMClient{Reader: baseCli, listErr: tc.listErr}

			exists, err := olm.SubscriptionExists(t.Context(), cli, "my-operator")
			require.ErrorIs(t, err, tc.listErr)
			assert.Equal(t, tc.want, exists)
		})
	}
}
