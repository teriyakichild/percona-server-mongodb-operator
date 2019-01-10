package backup

import (
	"errors"
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var mockError = errors.New("mock error")

func TestEnsureCoordinator(t *testing.T) {
	client := &mocks.Client{}
	c := &Controller{
		client: client,
		psmdb: &v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name(),
			},
			Spec: v1alpha1.PerconaServerMongoDBSpec{
				Backup: &v1alpha1.BackupSpec{
					Coordinator: &v1alpha1.BackupCoordinatorSpec{
						ResourcesSpec: &v1alpha1.ResourcesSpec{
							Limits: &v1alpha1.ResourceSpecRequirements{
								Cpu:     "1",
								Memory:  "1G",
								Storage: "1G",
							},
							Requests: &v1alpha1.ResourceSpecRequirements{
								Cpu:    "1",
								Memory: "1G",
							},
						},
					},
				},
			},
		},
	}

	t.Run("create", func(t *testing.T) {
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
		assert.NoError(t, c.EnsureCoordinator())

		// test failures
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(mockError).Once()
		assert.Error(t, c.EnsureCoordinator())
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(mockError).Once()
		assert.Error(t, c.EnsureCoordinator())
	})

	t.Run("update", func(t *testing.T) {
		err := k8serrors.NewAlreadyExists(schema.GroupResource{
			Group:    "group",
			Resource: "resource",
		}, t.Name())
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(err).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(err).Once()
		client.On("Update", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		client.On("Update", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
		assert.NoError(t, c.EnsureCoordinator())
	})
}

func TestDeleteCoordinator(t *testing.T) {
	client := &mocks.Client{}
	c := &Controller{
		client: client,
		psmdb: &v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name(),
			},
			Spec: v1alpha1.PerconaServerMongoDBSpec{
				Backup: &v1alpha1.BackupSpec{
					Coordinator: &v1alpha1.BackupCoordinatorSpec{
						ResourcesSpec: &v1alpha1.ResourcesSpec{
							Limits: &v1alpha1.ResourceSpecRequirements{
								Cpu:     "1",
								Memory:  "1G",
								Storage: "1G",
							},
							Requests: &v1alpha1.ResourceSpecRequirements{
								Cpu:    "1",
								Memory: "1G",
							},
						},
					},
				},
			},
		},
	}
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
	client.On("Delete", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
	assert.NoError(t, c.DeleteCoordinator())

	// test failures
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
	client.On("Delete", mock.AnythingOfType("*v1.StatefulSet")).Return(mockError).Once()
	assert.Error(t, c.DeleteCoordinator())
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(mockError).Once()
	assert.Error(t, c.DeleteCoordinator())
}