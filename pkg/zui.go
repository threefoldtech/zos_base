package pkg

//go:generate zbusc -module zui -version 0.0.1 -name zui -package stubs github.com/threefoldtech/zos_base/pkg+ZUI stubs/zui_stub.go

type ZUI interface {
	PushErrors(label string, errors []string) error
}
