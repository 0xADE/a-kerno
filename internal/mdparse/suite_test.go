package mdparse_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMdparse(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "mdparse Suite")
}
