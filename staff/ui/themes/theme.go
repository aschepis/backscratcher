package themes

import (
	"fmt"
	"math/rand"
	"slices"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type ThemeName string

const (
	ThemeSolarized     ThemeName = "solarized"
	ThemeGruvbox       ThemeName = "gruvbox"
	ThemeZenburn       ThemeName = "zenburn"
	ThemeApprentice    ThemeName = "apprentice"
	ThemeCyberpunk     ThemeName = "cyberpunk"
	ThemeCherryblossom ThemeName = "cherryblossom"
	ThemeRandom        ThemeName = "random"
)

var themeNames = []ThemeName{ThemeRandom, ThemeSolarized, ThemeGruvbox, ThemeZenburn, ThemeApprentice, ThemeCyberpunk, ThemeCherryblossom}

// Theme represents a color theme for tview applications
type Theme struct {
	PrimitiveBackgroundColor    tcell.Color
	ContrastBackgroundColor     tcell.Color
	MoreContrastBackgroundColor tcell.Color
	BorderColor                 tcell.Color
	TitleColor                  tcell.Color
	GraphicsColor               tcell.Color
	PrimaryTextColor            tcell.Color
	SecondaryTextColor          tcell.Color
	TertiaryTextColor           tcell.Color
	InverseTextColor            tcell.Color
	ContrastSecondaryTextColor  tcell.Color
}

func ApplyByName(app *tview.Application, themeNameStr string) error {
	themeName := ThemeName(themeNameStr)
	if !slices.Contains(themeNames, themeName) {
		return fmt.Errorf("invalid theme name: %s", themeNameStr)
	}
	theme := getThemeByName(themeName)
	theme.Apply(app)
	return nil
}

// NewRandom returns a new Theme configured with a random color palette
func NewRandom() *Theme {
	themeName := themeNames[rand.Intn(len(themeNames))]
	switch themeName {
	case ThemeSolarized:
		return NewSolarized()
	case ThemeGruvbox:
		return NewGruvbox()
	case ThemeZenburn:
		return NewZenburn()
	case ThemeApprentice:
		return NewApprentice()
	case ThemeCyberpunk:
		return NewCyberpunk()
	case ThemeCherryblossom:
		return NewCherryBlossom()
	}
	return NewSolarized()
}

// Apply applies the theme to tview.Styles (global styles).
// The app parameter is accepted for API consistency but styles are always applied globally.
func (t *Theme) Apply(app *tview.Application) {
	tview.Styles.PrimitiveBackgroundColor = t.PrimitiveBackgroundColor
	tview.Styles.ContrastBackgroundColor = t.ContrastBackgroundColor
	tview.Styles.MoreContrastBackgroundColor = t.MoreContrastBackgroundColor
	tview.Styles.BorderColor = t.BorderColor
	tview.Styles.TitleColor = t.TitleColor
	tview.Styles.GraphicsColor = t.GraphicsColor
	tview.Styles.PrimaryTextColor = t.PrimaryTextColor
	tview.Styles.SecondaryTextColor = t.SecondaryTextColor
	tview.Styles.TertiaryTextColor = t.TertiaryTextColor
	tview.Styles.InverseTextColor = t.InverseTextColor
	tview.Styles.ContrastSecondaryTextColor = t.ContrastSecondaryTextColor
}

// NewSolarized returns a new Theme configured with Solarized Dark colors
func NewSolarized() *Theme {
	// Solarized Dark color palette
	// Base colors
	base03 := tcell.NewRGBColor(0, 43, 54)    // Darkest background
	base02 := tcell.NewRGBColor(7, 54, 66)    // Dark background
	base01 := tcell.NewRGBColor(88, 110, 117) // Dark content
	base0 := tcell.NewRGBColor(131, 148, 150) // Bright content
	base1 := tcell.NewRGBColor(147, 161, 161) // Brighter content
	base2 := tcell.NewRGBColor(238, 232, 213) // Light background
	base3 := tcell.NewRGBColor(253, 246, 227) // Lightest background

	// Accent colors
	yellow := tcell.NewRGBColor(181, 137, 0) // Yellow
	cyan := tcell.NewRGBColor(42, 161, 152)  // Cyan

	return &Theme{
		PrimitiveBackgroundColor:    base03,
		ContrastBackgroundColor:     base02,
		MoreContrastBackgroundColor: base01,
		BorderColor:                 base0,
		TitleColor:                  base1,
		GraphicsColor:               base0,
		PrimaryTextColor:            base0,
		SecondaryTextColor:          yellow,
		TertiaryTextColor:           cyan,
		InverseTextColor:            base3,
		ContrastSecondaryTextColor:  base2,
	}
}

// NewGruvbox returns a new Theme configured with Gruvbox Dark colors
// Based on: https://github.com/morhetz/gruvbox
func NewGruvbox() *Theme {
	// Gruvbox Dark color palette
	bg0 := tcell.NewRGBColor(40, 40, 40)      // Background
	bg1 := tcell.NewRGBColor(60, 56, 54)      // Darker background
	bg2 := tcell.NewRGBColor(80, 73, 69)      // Even darker background
	fg0 := tcell.NewRGBColor(235, 219, 178)   // Foreground
	fg1 := tcell.NewRGBColor(251, 241, 199)   // Brighter foreground
	yellow := tcell.NewRGBColor(215, 153, 33) // Yellow
	aqua := tcell.NewRGBColor(104, 157, 106)  // Cyan/Aqua
	gray := tcell.NewRGBColor(146, 131, 116)  // Gray

	return &Theme{
		PrimitiveBackgroundColor:    bg0,
		ContrastBackgroundColor:     bg1,
		MoreContrastBackgroundColor: bg2,
		BorderColor:                 gray,
		TitleColor:                  fg1,
		GraphicsColor:               gray,
		PrimaryTextColor:            fg0,
		SecondaryTextColor:          yellow,
		TertiaryTextColor:           aqua,
		InverseTextColor:            fg1,
		ContrastSecondaryTextColor:  fg0,
	}
}

// NewZenburn returns a new Theme configured with Zenburn colors
// A low-contrast color scheme designed to be easy on the eyes
func NewZenburn() *Theme {
	// Zenburn color palette
	bg0 := tcell.NewRGBColor(63, 63, 63)       // Background
	bg1 := tcell.NewRGBColor(48, 48, 48)       // Darker background
	bg2 := tcell.NewRGBColor(39, 39, 39)       // Even darker background
	fg0 := tcell.NewRGBColor(220, 220, 204)    // Foreground
	fg1 := tcell.NewRGBColor(255, 255, 255)    // Brighter foreground
	yellow := tcell.NewRGBColor(227, 206, 171) // Yellow
	cyan := tcell.NewRGBColor(147, 224, 227)   // Cyan

	return &Theme{
		PrimitiveBackgroundColor:    bg0,
		ContrastBackgroundColor:     bg1,
		MoreContrastBackgroundColor: bg2,
		BorderColor:                 fg0,
		TitleColor:                  fg1,
		GraphicsColor:               fg0,
		PrimaryTextColor:            fg0,
		SecondaryTextColor:          yellow,
		TertiaryTextColor:           cyan,
		InverseTextColor:            fg1,
		ContrastSecondaryTextColor:  fg0,
	}
}

// NewApprentice returns a new Theme configured with Apprentice colors
// Based on: https://github.com/romainl/Apprentice
// A dark, low-contrast color scheme with focus on readability
func NewApprentice() *Theme {
	// Apprentice color palette
	bg0 := tcell.NewRGBColor(38, 38, 38)      // Background
	bg1 := tcell.NewRGBColor(28, 28, 28)      // Darker background
	bg2 := tcell.NewRGBColor(17, 17, 17)      // Even darker background
	fg0 := tcell.NewRGBColor(188, 188, 188)   // Foreground
	fg1 := tcell.NewRGBColor(255, 255, 255)   // Brighter foreground
	yellow := tcell.NewRGBColor(135, 135, 95) // Yellow
	cyan := tcell.NewRGBColor(95, 135, 135)   // Cyan
	gray := tcell.NewRGBColor(108, 108, 108)  // Gray

	return &Theme{
		PrimitiveBackgroundColor:    bg0,
		ContrastBackgroundColor:     bg1,
		MoreContrastBackgroundColor: bg2,
		BorderColor:                 gray,
		TitleColor:                  fg1,
		GraphicsColor:               gray,
		PrimaryTextColor:            fg0,
		SecondaryTextColor:          yellow,
		TertiaryTextColor:           cyan,
		InverseTextColor:            fg1,
		ContrastSecondaryTextColor:  fg0,
	}
}

// NewCyberpunk returns a new Theme configured with Cyberpunk colors
// A vibrant, high-contrast neon theme inspired by cyberpunk aesthetics
func NewCyberpunk() *Theme {
	// Cyberpunk color palette
	bg0 := tcell.NewRGBColor(16, 13, 35)      // Dark purple/black background
	bg1 := tcell.NewRGBColor(30, 29, 69)      // Darker background
	bg2 := tcell.NewRGBColor(12, 10, 25)      // Even darker background
	fg0 := tcell.NewRGBColor(0, 255, 156)     // Neon green foreground
	fg1 := tcell.NewRGBColor(0, 255, 255)     // Cyan foreground
	green := tcell.NewRGBColor(0, 255, 106)   // Neon green
	yellow := tcell.NewRGBColor(255, 255, 0)  // Neon yellow
	magenta := tcell.NewRGBColor(255, 0, 255) // Neon magenta
	cyan := tcell.NewRGBColor(0, 255, 255)    // Neon cyan

	return &Theme{
		PrimitiveBackgroundColor:    bg0,
		ContrastBackgroundColor:     bg1,
		MoreContrastBackgroundColor: bg2,
		BorderColor:                 cyan,
		TitleColor:                  fg1,
		GraphicsColor:               magenta,
		PrimaryTextColor:            fg0,
		SecondaryTextColor:          yellow,
		TertiaryTextColor:           cyan,
		InverseTextColor:            fg1,
		ContrastSecondaryTextColor:  green,
	}
}

// NewCherryBlossom returns a new Theme configured with Cherry Blossom colors
// Based on: https://github.com/inlinestyle/emacs-cherry-blossom-theme
// A soothing pink/soft color scheme inspired by cherry blossoms
func NewCherryBlossom() *Theme {
	// Cherry Blossom color palette
	// Soothing pink and soft pastel colors
	bg0 := tcell.NewRGBColor(39, 19, 6)          // Dark background (#271306)
	bg1 := tcell.NewRGBColor(56, 28, 18)         // Darker background (#381c12)
	bg2 := tcell.NewRGBColor(73, 37, 30)         // Even darker background (#49251e)
	fg0 := tcell.NewRGBColor(247, 206, 224)      // Soft pink foreground (#f7cee0)
	fg1 := tcell.NewRGBColor(255, 230, 245)      // Brighter pink foreground (#ffe6f5)
	pink := tcell.NewRGBColor(247, 206, 224)     // Soft pink (#f7cee0)
	rose := tcell.NewRGBColor(184, 116, 157)     // Rose/Magenta (#b8749d)
	cherry := tcell.NewRGBColor(210, 140, 175)   // Cherry pink (#d28caf)
	blossom := tcell.NewRGBColor(255, 192, 203)  // Light pink blossom (#ffc0cb)
	lavender := tcell.NewRGBColor(230, 200, 230) // Lavender (#e6c8e6)

	return &Theme{
		PrimitiveBackgroundColor:    bg0,
		ContrastBackgroundColor:     bg1,
		MoreContrastBackgroundColor: bg2,
		BorderColor:                 rose,
		TitleColor:                  fg1,
		GraphicsColor:               cherry,
		PrimaryTextColor:            fg0,
		SecondaryTextColor:          blossom,
		TertiaryTextColor:           lavender,
		InverseTextColor:            fg1,
		ContrastSecondaryTextColor:  pink,
	}
}

// getThemeByName returns a theme by name, defaulting to Solarized if invalid
func getThemeByName(themeName ThemeName) *Theme {
	switch themeName {
	case ThemeRandom:
		return NewRandom()
	case ThemeSolarized:
		return NewSolarized()
	case ThemeGruvbox:
		return NewGruvbox()
	case ThemeZenburn:
		return NewZenburn()
	case ThemeApprentice:
		return NewApprentice()
	case ThemeCyberpunk:
		return NewCyberpunk()
	case ThemeCherryblossom:
		return NewCherryBlossom()
	}
	return NewSolarized()
}
