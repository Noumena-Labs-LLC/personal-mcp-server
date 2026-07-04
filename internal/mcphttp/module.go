package mcphttp

// Module is a small registration unit for tools, resources, prompts, or
// related MCP capabilities. Prefer adding new features behind a Module instead
// of growing a single central registration function.
type Module interface {
	Register(*Server)
}

// ModuleFunc adapts a function into a Module.
type ModuleFunc func(*Server)

func (f ModuleFunc) Register(s *Server) { f(s) }

func (s *Server) RegisterModule(m Module) {
	if m != nil {
		m.Register(s)
	}
}
