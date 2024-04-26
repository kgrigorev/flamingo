// Code generated by mockery v2.42.3. DO NOT EDIT.

package mocks

import (
	context "context"

	domain "flamingo.me/flamingo/v3/core/security/domain"
	mock "github.com/stretchr/testify/mock"

	web "flamingo.me/flamingo/v3/framework/web"
)

// Provider is an autogenerated mock type for the Provider type
type Provider struct {
	mock.Mock
}

type Provider_Expecter struct {
	mock *mock.Mock
}

func (_m *Provider) EXPECT() *Provider_Expecter {
	return &Provider_Expecter{mock: &_m.Mock}
}

// All provides a mock function with given fields: _a0, _a1
func (_m *Provider) All(_a0 context.Context, _a1 *web.Session) []domain.Role {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for All")
	}

	var r0 []domain.Role
	if rf, ok := ret.Get(0).(func(context.Context, *web.Session) []domain.Role); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]domain.Role)
		}
	}

	return r0
}

// Provider_All_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'All'
type Provider_All_Call struct {
	*mock.Call
}

// All is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 *web.Session
func (_e *Provider_Expecter) All(_a0 interface{}, _a1 interface{}) *Provider_All_Call {
	return &Provider_All_Call{Call: _e.mock.On("All", _a0, _a1)}
}

func (_c *Provider_All_Call) Run(run func(_a0 context.Context, _a1 *web.Session)) *Provider_All_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*web.Session))
	})
	return _c
}

func (_c *Provider_All_Call) Return(_a0 []domain.Role) *Provider_All_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *Provider_All_Call) RunAndReturn(run func(context.Context, *web.Session) []domain.Role) *Provider_All_Call {
	_c.Call.Return(run)
	return _c
}

// NewProvider creates a new instance of Provider. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewProvider(t interface {
	mock.TestingT
	Cleanup(func())
}) *Provider {
	mock := &Provider{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
