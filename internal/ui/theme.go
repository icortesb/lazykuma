package ui

import "github.com/charmbracelet/lipgloss"

// Tokyo Night, as in mdg-tui.
const (
	colAccent = lipgloss.Color("#7aa2f7")
	colFg     = lipgloss.Color("#c0caf5")
	colMuted  = lipgloss.Color("#a9b1d6")
	colDim    = lipgloss.Color("#565f89")
	colOK     = lipgloss.Color("#9ece6a")
	colWarn   = lipgloss.Color("#e0af68")
	colErr    = lipgloss.Color("#f7768e")
	colMaint  = lipgloss.Color("#bb9af7")
)

var (
	styleLogo     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleSubtitle = lipgloss.NewStyle().Foreground(colDim).Italic(true)

	styleItem       = lipgloss.NewStyle().Foreground(colMuted)
	styleItemActive = lipgloss.NewStyle().Foreground(colFg).Background(colAccent).Bold(true)
	styleItemDesc   = lipgloss.NewStyle().Foreground(colDim).Italic(true)

	styleHeading = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleLabel   = lipgloss.NewStyle().Foreground(colDim)
	styleValue   = lipgloss.NewStyle().Foreground(colFg)
	styleKey     = lipgloss.NewStyle().Foreground(colWarn)
	styleFooter  = lipgloss.NewStyle().Foreground(colDim)

	styleOK    = lipgloss.NewStyle().Foreground(colOK)
	styleWarn  = lipgloss.NewStyle().Foreground(colWarn)
	styleErr   = lipgloss.NewStyle().Foreground(colErr)
	styleMaint = lipgloss.NewStyle().Foreground(colMaint)

	stylePanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim).Padding(0, 1)
	styleRow   = lipgloss.NewStyle().Foreground(colFg).Background(lipgloss.Color("#283457"))
)

// The block letters are 69 columns wide; the compact ones are for a
// terminal too narrow to hold them.
var logoBlock = []string{
	`██╗      █████╗ ███████╗██╗   ██╗██╗  ██╗██╗   ██╗███╗   ███╗ █████╗ `,
	`██║     ██╔══██╗╚══███╔╝╚██╗ ██╔╝██║ ██╔╝██║   ██║████╗ ████║██╔══██╗`,
	`██║     ███████║  ███╔╝  ╚████╔╝ █████╔╝ ██║   ██║██╔████╔██║███████║`,
	`██║     ██╔══██║ ███╔╝    ╚██╔╝  ██╔═██╗ ██║   ██║██║╚██╔╝██║██╔══██║`,
	`███████╗██║  ██║███████╗   ██║   ██║  ██╗╚██████╔╝██║ ╚═╝ ██║██║  ██║`,
	`╚══════╝╚═╝  ╚═╝╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝╚═╝  ╚═╝`,
}

var logoCompact = []string{
	`╦  ╔═╗╔═╗╦ ╦╦╔═╦ ╦╔╦╗╔═╗`,
	`║  ╠═╣╔═╝╚╦╝╠╩╗║ ║║║║╠═╣`,
	`╩═╝╩ ╩╚═╝ ╩ ╩ ╩╚═╝╩ ╩╩ ╩`,
}

func logoFor(width int) []string {
	if width < 75 {
		return logoCompact
	}
	return logoBlock
}

func center(width int, s string) string {
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, s)
}
