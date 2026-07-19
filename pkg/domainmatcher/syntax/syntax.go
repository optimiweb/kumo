package syntax

import (
	"github.com/optimiweb/kumo/pkg/domainmatcher/syntax/ast"
	"github.com/optimiweb/kumo/pkg/domainmatcher/syntax/lexer"
)

func Parse(s string) (*ast.Node, error) {
	return ast.Parse(lexer.NewLexer(s))
}

func Special(b byte) bool {
	return lexer.Special(b)
}
