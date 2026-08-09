// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	presetsPrefKey  = "saved_times"
	hostPrefKey     = "obs_host"
	passwordPrefKey = "obs_password"

	defaultOBSHost = "localhost:4455"
)

type App struct {
	fyneApp fyne.App
	window  fyne.Window
	obs     *OBSController

	hostEntry     *widget.Entry
	passwordEntry *widget.Entry
	connectBtn    *widget.Button
	connStatus    *widget.Label

	timeEntry   *widget.Entry
	statusLabel *widget.Label
	recordBtn   *widget.Button
	stopBtn     *widget.Button
	presetsBox  *fyne.Container // ที่เก็บปุ่ม preset ทั้งหมด ไว้ rebuild ใหม่ทุกครั้งที่แก้ list

	stopRequested chan struct{}
	recording     bool
}

func main() {
	a := app.NewWithID("com.pip-recorder.app")
	w := a.NewWindow("PiP Recorder")

	myApp := &App{
		fyneApp: a,
		window:  w,
	}
	myApp.buildUI()
	myApp.tryAutoConnect()

	w.Resize(fyne.NewSize(380, 420))
	w.ShowAndRun()
}

func (a *App) buildUI() {
	// ==== ส่วนเชื่อมต่อ OBS ====
	savedHost := a.fyneApp.Preferences().StringWithFallback(hostPrefKey, defaultOBSHost)
	savedPassword := a.fyneApp.Preferences().String(passwordPrefKey)

	a.hostEntry = widget.NewEntry()
	a.hostEntry.SetText(savedHost)
	a.hostEntry.SetPlaceHolder("localhost:4455")

	a.passwordEntry = widget.NewPasswordEntry()
	a.passwordEntry.SetText(savedPassword)
	a.passwordEntry.SetPlaceHolder("OBS WebSocket password")

	a.connStatus = widget.NewLabel("ยังไม่ได้เชื่อมต่อ OBS")
	a.connectBtn = widget.NewButton("เชื่อมต่อ OBS", a.onConnectPressed)

	connBox := container.NewVBox(
		widget.NewLabel("OBS Host:"),
		a.hostEntry,
		widget.NewLabel("OBS Password:"),
		a.passwordEntry,
		a.connectBtn,
		a.connStatus,
	)

	// ==== ส่วนตั้งเวลา/บันทึก ====
	a.timeEntry = widget.NewEntry()
	a.timeEntry.SetPlaceHolder("mm:ss เช่น 10:30")

	a.statusLabel = widget.NewLabel("พร้อมทำงาน")
	a.statusLabel.Wrapping = fyne.TextWrapWord

	a.recordBtn = widget.NewButton("Record", a.onRecordPressed)
	a.stopBtn = widget.NewButton("หยุดเอง", a.onStopPressed)
	a.stopBtn.Disable()

	saveBtn := widget.NewButtonWithIcon("บันทึกเวลานี้", theme.ContentAddIcon(), a.onSavePresetPressed)

	a.presetsBox = container.NewGridWrap(fyne.NewSize(90, 36))

	content := container.NewVBox(
		connBox,
		widget.NewSeparator(),
		widget.NewLabel("เวลาโดยประมาณ (mm:ss):"),
		a.timeEntry,
		saveBtn,
		widget.NewLabel("เวลาที่บันทึกไว้:"),
		a.presetsBox,
		widget.NewSeparator(),
		container.NewHBox(a.recordBtn, a.stopBtn),
		widget.NewSeparator(),
		a.statusLabel,
	)
	a.window.SetContent(content)

	a.refreshPresets()
}

// tryAutoConnect ลองเชื่อมต่อ OBS อัตโนมัติตอนเปิดแอพ ถ้ามี host/password บันทึกไว้แล้ว
func (a *App) tryAutoConnect() {
	if strings.TrimSpace(a.hostEntry.Text) == "" {
		return
	}
	a.connectToOBS()
}

func (a *App) onConnectPressed() {
	a.connectToOBS()
}

func (a *App) connectToOBS() {
	host := strings.TrimSpace(a.hostEntry.Text)
	password := a.passwordEntry.Text

	if host == "" {
		dialog.ShowError(fmt.Errorf("กรอก OBS host ก่อน (เช่น localhost:4455)"), a.window)
		return
	}

	if a.obs != nil {
		a.obs.Close()
		a.obs = nil
	}

	obs, err := NewOBSController(host, password)
	if err != nil {
		a.obs = nil
		a.connStatus.SetText("เชื่อมต่อไม่สำเร็จ")
		dialog.ShowError(err, a.window)
		return
	}

	a.obs = obs
	a.connStatus.SetText("เชื่อมต่อ OBS สำเร็จ (" + host + ")")

	// เชื่อมต่อสำเร็จแล้วค่อยเซฟไว้ ครั้งหน้าจะได้ auto-connect ได้เลย
	a.fyneApp.Preferences().SetString(hostPrefKey, host)
	a.fyneApp.Preferences().SetString(passwordPrefKey, password)
}

// ==== Preset เวลา ====

func (a *App) loadPresets() []string {
	return a.fyneApp.Preferences().StringList(presetsPrefKey)
}

func (a *App) savePresetsList(presets []string) {
	a.fyneApp.Preferences().SetStringList(presetsPrefKey, presets)
}

func (a *App) onSavePresetPressed() {
	text := strings.TrimSpace(a.timeEntry.Text)
	if _, err := parseMMSS(text); err != nil {
		dialog.ShowError(fmt.Errorf("กรอกเวลาในรูปแบบ mm:ss ก่อนบันทึก: %w", err), a.window)
		return
	}

	presets := a.loadPresets()
	for _, p := range presets {
		if p == text {
			// มีอยู่แล้ว ไม่ต้องเพิ่มซ้ำ
			return
		}
	}
	presets = append(presets, text)
	sort.Slice(presets, func(i, j int) bool {
		di, _ := parseMMSS(presets[i])
		dj, _ := parseMMSS(presets[j])
		return di < dj
	})
	a.savePresetsList(presets)
	a.refreshPresets()
}

func (a *App) deletePreset(value string) {
	presets := a.loadPresets()
	out := make([]string, 0, len(presets))
	for _, p := range presets {
		if p != value {
			out = append(out, p)
		}
	}
	a.savePresetsList(out)
	a.refreshPresets()
}

// refreshPresets สร้างปุ่ม preset ใหม่ทั้งหมดตาม list ปัจจุบัน แต่ละอันมีปุ่มลบ (x) กำกับ
func (a *App) refreshPresets() {
	presets := a.loadPresets()
	a.presetsBox.Objects = nil

	for _, p := range presets {
		value := p // capture ไว้ใช้ใน closure
		applyBtn := widget.NewButton(value, func() {
			a.timeEntry.SetText(value)
		})
		delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			a.deletePreset(value)
		})
		delBtn.Importance = widget.LowImportance
		row := container.NewBorder(nil, nil, nil, delBtn, applyBtn)
		a.presetsBox.Add(row)
	}
	a.presetsBox.Refresh()
}

// ==== Recording flow ====

func (a *App) setStatus(s string) {
	fyne.Do(func() {
		a.statusLabel.SetText(s)
	})
}

func (a *App) onRecordPressed() {
	if a.recording {
		return
	}
	dur, err := parseMMSS(a.timeEntry.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("กรอกเวลาไม่ถูกต้อง (ใช้รูปแบบ mm:ss): %w", err), a.window)
		return
	}
	if a.obs == nil {
		dialog.ShowError(fmt.Errorf("ยังไม่ได้เชื่อมต่อ OBS สำเร็จ (กด \"เชื่อมต่อ OBS\" ก่อน)"), a.window)
		return
	}

	if err := a.obs.StartRecord(); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	a.recording = true
	a.stopRequested = make(chan struct{})
	a.recordBtn.Disable()
	a.stopBtn.Enable()
	a.setStatus("เริ่มอัดแล้ว กำลังนับเวลา...")

	go a.runRecordingFlow(dur)
}

func (a *App) onStopPressed() {
	if !a.recording {
		return
	}
	close(a.stopRequested)
}

func (a *App) runRecordingFlow(timerDur time.Duration) {
	if a.waitTimer(timerDur) {
		a.finishRecording("ครบเวลาแล้ว หยุดอัดอัตโนมัติ")
	}
}

// waitTimer นับถอยหลัง คืนค่า true ถ้านับจนครบ, false ถ้าโดนสั่งหยุดเองก่อน
func (a *App) waitTimer(dur time.Duration) bool {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	remaining := dur

	for remaining > 0 {
		select {
		case <-a.stopRequested:
			a.finishRecording("หยุดอัดด้วยตนเอง")
			return false
		case <-ticker.C:
			remaining -= time.Second
			a.setStatus(fmt.Sprintf("กำลังนับเวลา... เหลือ %s", formatDuration(remaining)))
		}
	}
	return true
}

func (a *App) finishRecording(reason string) {
	if a.obs != nil {
		if err := a.obs.StopRecord(); err != nil {
			a.setStatus(fmt.Sprintf("หยุดอัดผิดพลาด: %v", err))
			return
		}
	}
	a.recording = false
	fyne.Do(func() {
		a.recordBtn.Enable()
		a.stopBtn.Disable()
	})
	a.setStatus(reason + " (หยุดอัดแล้ว)")
}

func parseMMSS(s string) (time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("รูปแบบต้องเป็น mm:ss")
	}
	mm, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	ss, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	return time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second, nil
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02d:%02d", m, s)
}
