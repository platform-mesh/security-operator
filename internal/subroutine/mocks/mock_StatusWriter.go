// Code generated manually for testing purposes.

package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MockStatusWriter is a mock implementation of client.StatusWriter
type MockStatusWriter struct {
	mock.Mock
}

func NewMockStatusWriter(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockStatusWriter {
	m := &MockStatusWriter{}
	m.Mock.Test(t)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

func (m *MockStatusWriter) EXPECT() *MockStatusWriter_Expecter {
	return &MockStatusWriter_Expecter{mock: &m.Mock}
}

type MockStatusWriter_Expecter struct {
	mock *mock.Mock
}

func (m *MockStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	args := m.Called(ctx, obj, subResource, opts)
	return args.Error(0)
}

func (m *MockStatusWriter_Expecter) Create(ctx interface{}, obj interface{}, subResource interface{}, opts ...interface{}) *MockStatusWriter_Create_Call {
	return &MockStatusWriter_Create_Call{Call: m.mock.On("Create", ctx, obj, subResource, opts)}
}

type MockStatusWriter_Create_Call struct {
	*mock.Call
}

func (c *MockStatusWriter_Create_Call) Return(_a0 error) *MockStatusWriter_Create_Call {
	c.Call.Return(_a0)
	return c
}

func (c *MockStatusWriter_Create_Call) RunAndReturn(run func(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error) *MockStatusWriter_Create_Call {
	c.Call.Return(run)
	return c
}

func (m *MockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	args := m.Called(ctx, obj, opts)
	return args.Error(0)
}

func (m *MockStatusWriter_Expecter) Update(ctx interface{}, obj interface{}, opts ...interface{}) *MockStatusWriter_Update_Call {
	return &MockStatusWriter_Update_Call{Call: m.mock.On("Update", append([]interface{}{ctx, obj}, opts...)...)}
}

type MockStatusWriter_Update_Call struct {
	*mock.Call
}

func (c *MockStatusWriter_Update_Call) Return(_a0 error) *MockStatusWriter_Update_Call {
	c.Call.Return(_a0)
	return c
}

func (c *MockStatusWriter_Update_Call) RunAndReturn(run func(context.Context, client.Object, ...client.SubResourceUpdateOption) error) *MockStatusWriter_Update_Call {
	c.Call.Return(run)
	return c
}

func (c *MockStatusWriter_Update_Call) Once() *MockStatusWriter_Update_Call {
	c.Call.Once()
	return c
}

func (m *MockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	args := m.Called(ctx, obj, patch, opts)
	return args.Error(0)
}

func (m *MockStatusWriter_Expecter) Patch(ctx interface{}, obj interface{}, patch interface{}, opts ...interface{}) *MockStatusWriter_Patch_Call {
	return &MockStatusWriter_Patch_Call{Call: m.mock.On("Patch", ctx, obj, patch, opts)}
}

type MockStatusWriter_Patch_Call struct {
	*mock.Call
}

func (c *MockStatusWriter_Patch_Call) Return(_a0 error) *MockStatusWriter_Patch_Call {
	c.Call.Return(_a0)
	return c
}

func (c *MockStatusWriter_Patch_Call) RunAndReturn(run func(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error) *MockStatusWriter_Patch_Call {
	c.Call.Return(run)
	return c
}
