package mdparse_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/0xADE/a-kerno/internal/mdparse"
)

var _ = Describe("mdparse", func() {
	It("parses enabled daemons and property sections", func() {
		markdown := []byte(`## enabled daemons
- [x] a-lancxo
- [ ] a-tondujo

## a-lancxo properties
- exec: /usr/local/bin/a-lancxo
- order: 10
- restart: on-failure
`)

		sections, err := mdparse.Parse(markdown)
		Expect(err).NotTo(HaveOccurred())

		enabled := sections["enabled daemons"]
		Expect(enabled).NotTo(BeNil())
		Expect(enabled.Enabled).To(HaveKeyWithValue("a-lancxo", true))
		Expect(enabled.Enabled).To(HaveKeyWithValue("a-tondujo", false))

		lancxo := sections["a-lancxo properties"]
		Expect(lancxo).NotTo(BeNil())
		Expect(lancxo.Properties).To(HaveKeyWithValue("exec", "/usr/local/bin/a-lancxo"))
		Expect(lancxo.Properties).To(HaveKeyWithValue("order", "10"))
		Expect(lancxo.Properties).To(HaveKeyWithValue("restart", "on-failure"))
	})

	It("parses flat root lists for autostart files", func() {
		markdown := []byte(`- exec: /usr/bin/swaybg -i wallpaper.jpg
- phase: early
- enabled: true
`)

		props, _, err := mdparse.ParseRootLists(markdown)
		Expect(err).NotTo(HaveOccurred())
		Expect(props).To(HaveKeyWithValue("exec", "/usr/bin/swaybg -i wallpaper.jpg"))
		Expect(props).To(HaveKeyWithValue("phase", "early"))
		Expect(props).To(HaveKeyWithValue("enabled", "true"))
	})
})
