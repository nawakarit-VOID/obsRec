// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type MyTheme struct{}

var (
	bgDark       = color.NRGBA{32, 34, 39, 255}
	surfaceDark  = color.NRGBA{44, 48, 55, 255}
	hoverDark    = color.NRGBA{255, 255, 255, 18}
	inputDark    = color.NRGBA{255, 255, 255, 14}
	menuDark     = color.NRGBA{52, 57, 66, 255}
	overlayDark  = color.NRGBA{24, 26, 31, 240}
	textLight    = color.NRGBA{245, 246, 248, 255}
	primaryAmber = color.NRGBA{244, 196, 92, 255}
	primarySoft  = color.NRGBA{244, 196, 92, 128}
	errorRed     = color.NRGBA{225, 87, 89, 255}
	successGreen = color.NRGBA{83, 187, 132, 255}
	warningAmber = color.NRGBA{255, 184, 77, 255}
	bgLight      = color.NRGBA{248, 246, 241, 255}
	surfaceLight = color.NRGBA{255, 255, 255, 255}
	hoverLight   = color.NRGBA{0, 0, 0, 10}
	inputLight   = color.NRGBA{0, 0, 0, 7}
	menuLight    = color.NRGBA{255, 255, 255, 255}
	overlayLight = color.NRGBA{235, 232, 226, 245}
	textDark     = color.NRGBA{25, 27, 31, 255}
	disabledTone = color.NRGBA{155, 160, 168, 255}
	focusTone    = color.NRGBA{244, 196, 92, 64}
)

func (m MyTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if v == theme.VariantDark {
		switch name {
		case theme.ColorNameBackground:
			return bgDark
		case theme.ColorNameForeground:
			return textLight
		case theme.ColorNameButton:
			return surfaceDark
		case theme.ColorNamePressed:
			return primaryAmber
		case theme.ColorNameHover:
			return hoverDark
		case theme.ColorNameDisabledButton:
			return surfaceDark
		case theme.ColorNameDisabled:
			return disabledTone
		case theme.ColorNameFocus:
			return focusTone
		case theme.ColorNamePrimary:
			return primaryAmber
		case theme.ColorNameInputBackground:
			return inputDark
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{195, 199, 207, 255}
		case theme.ColorNameMenuBackground:
			return menuDark
		case theme.ColorNameOverlayBackground:
			return overlayDark
		case theme.ColorNameShadow:
			return primarySoft
		case theme.ColorNameError:
			return errorRed
		case theme.ColorNameSuccess:
			return successGreen
		case theme.ColorNameWarning:
			return warningAmber
		}
	} else {
		switch name {
		case theme.ColorNameBackground:
			return bgLight
		case theme.ColorNameForeground:
			return textDark
		case theme.ColorNameButton:
			return color.NRGBA{47, 52, 63, 14}
		case theme.ColorNamePressed:
			return primaryAmber
		case theme.ColorNameHover:
			return hoverLight
		case theme.ColorNameDisabledButton:
			return color.NRGBA{150, 154, 160, 90}
		case theme.ColorNameDisabled:
			return disabledTone
		case theme.ColorNameFocus:
			return focusTone
		case theme.ColorNamePrimary:
			return primaryAmber
		case theme.ColorNameInputBackground:
			return inputLight
		case theme.ColorNamePlaceHolder:
			return color.NRGBA{120, 124, 132, 255}
		case theme.ColorNameMenuBackground:
			return menuLight
		case theme.ColorNameOverlayBackground:
			return overlayLight
		case theme.ColorNameShadow:
			return primarySoft
		case theme.ColorNameError:
			return errorRed
		case theme.ColorNameSuccess:
			return successGreen
		case theme.ColorNameWarning:
			return warningAmber
		}
	}
	return theme.DefaultTheme().Color(name, v)
}

// ต้องมีครบ
func (m MyTheme) Font(s fyne.TextStyle) fyne.Resource {
	return myFont
	//return theme.DefaultTheme().Font(s)
}
func (m MyTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}
func (m MyTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {

	// 📏 spacing / ระยะ
	case theme.SizeNamePadding: // → ระยะห่างทั่วไป (margin/padding ของ widget)
		return 4
	case theme.SizeNameSeparatorThickness: // → ความหนาเส้นคั่น
		return 1

	// 🖼️ ไอคอน / scrollbar
	case theme.SizeNameInlineIcon: // → ขนาด icon ในปุ่ม/ข้อความ /dialog
		return 19

	case theme.SizeNameScrollBar: // → ความกว้าง scrollbar ปกติ
		return 12
	case theme.SizeNameScrollBarSmall: // → scrollbar แบบเล็ก
		return 3

	// 🔤 ขนาดตัวอักษร
	case theme.SizeNameText: // → ข้อความปกติ
		return 14
	case theme.SizeNameHeadingText: // → หัวข้อใหญ่
		return 20
	case theme.SizeNameSubHeadingText: // → หัวข้อรอง
		return 16
	case theme.SizeNameCaptionText: // → ตัวเล็ก (caption/คำอธิบาย)
		return 12

	// 🧾 input
	case theme.SizeNameInputBorder: // → ความหนาขอบ input
		return 1
	}
	return theme.DefaultTheme().Size(name)
}
