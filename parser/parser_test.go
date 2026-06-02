package parser

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parser", func() {

	Describe("NewParser", func() {
		It("parses a valid TXT01 header", func() {
			input := "TXT01\n"
			p, err := NewParser(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Header()).To(Equal("TXT"))
			Expect(p.Version()).To(Equal("01"))
		})

		It("rejects an invalid header format", func() {
			input := "BIN01\n"
			_, err := NewParser(strings.NewReader(input))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported format"))
		})

		It("rejects a short header", func() {
			input := "TX\n"
			_, err := NewParser(strings.NewReader(input))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ParseCommand", func() {
		var (
			input    string
			reader   *strings.Reader
			parser   *Parser
			cmd      *Command
			parseErr error
		)

		JustBeforeEach(func() {
			reader = strings.NewReader(input)
			var err error
			parser, err = NewParser(reader)
			Expect(err).NotTo(HaveOccurred())

			cmd, parseErr = parser.ParseCommand()
		})

		Context("when parsing a command with string arguments", func() {
			BeforeEach(func() {
				input = `TXT01
"/usr/local/bin/a-lancxo
"10
"on-failure
start
`
			})

			It("parses command name correctly", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Name).To(Equal("start"))
			})

			It("parses three arguments", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Args).To(HaveLen(3))
			})

			It("parses first argument as string", func() {
				Expect(cmd.Args[0].Type).To(Equal(TypeString))
				Expect(cmd.Args[0].Str).To(Equal("/usr/local/bin/a-lancxo"))
			})

			It("parses second argument as string", func() {
				Expect(cmd.Args[1].Type).To(Equal(TypeString))
				Expect(cmd.Args[1].Str).To(Equal("10"))
			})
		})

		Context("when parsing a command with integer arguments", func() {
			BeforeEach(func() {
				input = `TXT01
42
100
status
`
			})

			It("parses integer arguments", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Name).To(Equal("status"))
				Expect(cmd.Args).To(HaveLen(2))
				Expect(cmd.Args[0].Type).To(Equal(TypeInt))
				Expect(cmd.Args[0].Int).To(Equal(int64(42)))
				Expect(cmd.Args[1].Int).To(Equal(int64(100)))
			})
		})

		Context("when parsing a command with boolean arguments", func() {
			BeforeEach(func() {
				input = `TXT01
t
f
list
`
			})

			It("parses boolean arguments", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Args).To(HaveLen(2))
				Expect(cmd.Args[0].Type).To(Equal(TypeBool))
				Expect(cmd.Args[0].Bool).To(BeTrue())
				Expect(cmd.Args[1].Type).To(Equal(TypeBool))
				Expect(cmd.Args[1].Bool).To(BeFalse())
			})
		})

		Context("when parsing a command without arguments", func() {
			BeforeEach(func() {
				input = `TXT01
list
`
			})

			It("returns a command with empty args", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Name).To(Equal("list"))
				Expect(cmd.Args).To(BeEmpty())
			})
		})

		Context("when input has comments and blank lines", func() {
			BeforeEach(func() {
				input = `TXT01
# This is a comment
"my-daemon

stop
`
			})

			It("skips comments and blank lines", func() {
				Expect(parseErr).NotTo(HaveOccurred())
				Expect(cmd.Name).To(Equal("stop"))
				Expect(cmd.Args).To(HaveLen(1))
				Expect(cmd.Args[0].Str).To(Equal("my-daemon"))
			})
		})

		Context("when parsing multiple commands", func() {
			It("reads all commands via ReadAllCommands", func() {
				input = `TXT01
"daemon1
start
"daemon2
stop
`
				reader = strings.NewReader(input)
				var err error
				parser, err = NewParser(reader)
				Expect(err).NotTo(HaveOccurred())

				cmds, err := parser.ReadAllCommands()
				Expect(err).NotTo(HaveOccurred())
				Expect(cmds).To(HaveLen(2))
				Expect(cmds[0].Name).To(Equal("start"))
				Expect(cmds[1].Name).To(Equal("stop"))
			})
		})
	})

	Describe("resolveCommand", func() {
		It("recognises lifecycle commands", func() {
			Expect(resolveCommand("start")).To(Equal("start"))
			Expect(resolveCommand("stop")).To(Equal("stop"))
			Expect(resolveCommand("restart")).To(Equal("restart"))
			Expect(resolveCommand("status")).To(Equal("status"))
		})

		It("recognises query commands", func() {
			Expect(resolveCommand("list")).To(Equal("list"))
			Expect(resolveCommand("reload")).To(Equal("reload"))
		})

		It("recognises program management commands", func() {
			Expect(resolveCommand("prog-start")).To(Equal("prog-start"))
			Expect(resolveCommand("prog-stop")).To(Equal("prog-stop"))
		})

		It("recognises generic commands", func() {
			Expect(resolveCommand("help")).To(Equal("help"))
			Expect(resolveCommand("version")).To(Equal("version"))
			Expect(resolveCommand("quit")).To(Equal("quit"))
		})

		It("returns empty for unknown commands", func() {
			Expect(resolveCommand("unknown")).To(BeEmpty())
			Expect(resolveCommand("")).To(BeEmpty())
		})
	})

	Describe("FormatResponse", func() {
		It("formats an OK response", func() {
			resp := FormatOK("daemon started")
			Expect(resp).To(ContainSubstring("20"))
			Expect(resp).To(ContainSubstring("daemon started"))
		})

		It("formats an ERROR response", func() {
			resp := FormatError("unknown daemon")
			Expect(resp).To(ContainSubstring("50"))
			Expect(resp).To(ContainSubstring("unknown daemon"))
		})
	})
})
