package cli

import (
	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/spf13/cobra"
)

const usageTemplate = `使い方:
  {{.UseLine}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} コマンド [オプション]

コマンド:{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

オプション:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

共通オプション:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`

func localize(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(`{{with (or .Long .Short)}}{{.}}

{{end}}{{.UsageString}}`)
	root.SetHelpCommand(&cobra.Command{Use: "help [COMMAND]", Short: "コマンドの使い方を表示", RunE: func(cmd *cobra.Command, args []string) error {
		found, _, e := root.Find(args)
		if e != nil {
			return api.Err("usage", "コマンドが見つかりません")
		}
		return found.Help()
	}})
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		if f := c.Flags().Lookup("help"); f != nil {
			f.Usage = "使い方を表示"
		}
		if args := c.Args; args != nil {
			c.Args = func(cmd *cobra.Command, v []string) error {
				if e := args(cmd, v); e != nil {
					return api.Err("usage", "引数の数が不正です。--help で使い方を確認してください")
				}
				return nil
			}
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	root.InitDefaultVersionFlag()
	if f := root.Flags().Lookup("version"); f != nil {
		f.Usage = "バージョンを表示"
	}
	root.SetVersionTemplate("file-okiba {{.Version}}\n")
}
