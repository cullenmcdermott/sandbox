package main

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// themedStyleConfig derives a glamour StyleConfig from the active theme tokens,
// starting from the stock dark style and overriding only the color-bearing
// fields. This is the fix for "the markdown coloring doesn't fit my theme":
// today's renderer hardcodes glamour.WithStandardStyle("dark"), whose headings
// (ANSI 39), inline code (203 on 236), and H1 purple block have nothing to do
// with the Midnight palette. Read tokens at call time so a /theme swap reskins.
func themedStyleConfig() ansi.StyleConfig {
	c := gstyles.DarkStyleConfig // value copy; we only reassign pointer fields

	c.Document.Color = sp(hexOf(theme.TextBody))
	c.Document.Margin = up(0)

	// Headings: brand color encodes level; drop the loud H1 background block AND
	// the literal "## " / "### " prefixes glamour's dark style keeps — those
	// visible hashes are a big part of why the output reads as "unrendered". A
	// thin Charple bar anchors H2 without shouting; deeper levels lean on color.
	c.Heading.Color, c.Heading.Bold = sp(hexOf(theme.Charple)), bp(true)
	c.H1.Prefix, c.H1.Suffix = "", ""
	c.H1.BackgroundColor, c.H1.Color, c.H1.Bold = nil, sp(hexOf(theme.TextBright)), bp(true)
	c.H2.Prefix, c.H2.Color, c.H2.Bold = "▌ ", sp(hexOf(theme.Charple)), bp(true)
	c.H3.Prefix, c.H3.Color, c.H3.Bold = "", sp(hexOf(theme.Hazy)), bp(true)
	c.H4.Prefix, c.H4.Color, c.H4.Bold = "", sp(hexOf(theme.Malibu)), bp(true)

	// Inline code: a quiet Raised2 chip with Peach text instead of salmon-on-grey.
	c.Code.Color, c.Code.BackgroundColor = sp(hexOf(theme.Peach)), sp(hexOf(theme.Raised2))

	c.Link.Color, c.Link.Underline = sp(hexOf(theme.Malibu)), bp(true)
	c.LinkText.Color, c.LinkText.Bold = sp(hexOf(theme.Malibu)), bp(true)

	c.BlockQuote.Color, c.BlockQuote.Italic = sp(hexOf(theme.TextMuted)), bp(true)
	c.BlockQuote.IndentToken = sp("│ ")

	c.Emph.Color = sp(hexOf(theme.TextBright))
	c.Strong.Color = sp(hexOf(theme.TextBright))
	c.HorizontalRule.Color = sp(hexOf(theme.TextDim))

	c.CodeBlock.Color = sp(hexOf(theme.TextBody))
	c.CodeBlock.Margin = up(1)
	c.CodeBlock.Chroma = themedChroma()
	return c
}

// themedChroma maps chroma syntax classes onto theme accents so fenced code
// reads as part of the palette (Hazy keywords, Guac functions, Gold strings,
// Malibu numbers/builtins) rather than glamour's stock rainbow.
func themedChroma() *ansi.Chroma {
	p := func(c color.Color) ansi.StylePrimitive { return ansi.StylePrimitive{Color: sp(hexOf(c))} }
	return &ansi.Chroma{
		Text:                p(theme.TextBody),
		Comment:             p(theme.TextMuted),
		CommentPreproc:      p(theme.Peach),
		Keyword:             p(theme.Hazy),
		KeywordReserved:     p(theme.Dolly),
		KeywordNamespace:    p(theme.Dolly),
		KeywordType:         p(theme.Malibu),
		Operator:            p(theme.TextSecondary),
		Punctuation:         p(theme.TextSecondary),
		Name:                p(theme.TextBody),
		NameBuiltin:         p(theme.Malibu),
		NameTag:             p(theme.Hazy),
		NameAttribute:       p(theme.Malibu),
		NameClass:           ansi.StylePrimitive{Color: sp(hexOf(theme.TextBright)), Bold: bp(true)},
		NameDecorator:       p(theme.Gold),
		NameFunction:        p(theme.Guac),
		NameOther:           p(theme.TextBody),
		LiteralNumber:       p(theme.Malibu),
		LiteralString:       p(theme.Gold),
		LiteralStringEscape: p(theme.Peach),
		GenericDeleted:      p(theme.Coral),
		GenericInserted:     p(theme.Guac),
		GenericEmph:         ansi.StylePrimitive{Italic: bp(true)},
		GenericStrong:       ansi.StylePrimitive{Bold: bp(true)},
		GenericSubheading:   p(theme.TextMuted),
		Background:          ansi.StylePrimitive{BackgroundColor: sp(hexOf(theme.Surface))},
		Error:               ansi.StylePrimitive{Color: sp(hexOf(theme.TextBright)), BackgroundColor: sp(hexOf(theme.Coral))},
	}
}

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }
func up(u uint) *uint     { return &u }

// hexOf renders a theme color.Color as the "#RRGGBB" string glamour expects.
func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
