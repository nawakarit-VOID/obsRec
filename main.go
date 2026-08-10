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
	logBox      *widget.Entry
	recordBtn   *widget.Button
	stopBtn     *widget.Button
	presetsBox  *fyne.Container // ที่เก็บปุ่ม preset ทั้งหมด ไว้ rebuild ใหม่ทุกครั้งที่แก้ list
	sourcesBox  *fyne.Container // รายการ audio stream ทั้งหมด แบบ toggle ✅/⬜

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

	w.Resize(fyne.NewSize(420, 620))
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

	a.logBox = widget.NewMultiLineEntry()
	a.logBox.Wrapping = fyne.TextWrapWord
	a.logBox.SetMinRowsVisible(6)

	a.recordBtn = widget.NewButton("Record", a.onRecordPressed)
	a.stopBtn = widget.NewButton("หยุดเอง", a.onStopPressed)
	a.stopBtn.Disable()

	saveBtn := widget.NewButtonWithIcon("บันทึกเวลานี้", theme.ContentAddIcon(), a.onSavePresetPressed)

	a.presetsBox = container.NewGridWrap(fyne.NewSize(90, 36))

	// ==== ส่วน audio routing ====
	a.sourcesBox = container.NewVBox()
	scanBtn := widget.NewButton("สแกนแหล่งเสียง", a.scan)
	autoBtn := widget.NewButton("เชื่อมอัตโนมัติจาก PiP", a.autoConnectFromPiP)
	autoBtn.Importance = widget.HighImportance

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
		a.statusLabel,
		widget.NewSeparator(),
		widget.NewLabel("Audio routing (คลิกที่ชิปเพื่อเชื่อม/ยกเลิก):"),
		container.NewHBox(scanBtn, autoBtn),
		a.sourcesBox,
		widget.NewSeparator(),
		widget.NewLabel("Log:"),
		container.NewScroll(a.logBox),
	)
	a.window.SetContent(content)

	a.refreshPresets()
}

func (a *App) appendLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fyne.Do(func() {
		a.logBox.SetText(a.logBox.Text + msg + "\n")
	})
}

// ==== เชื่อมต่อ OBS ====

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

func (a *App) refreshPresets() {
	presets := a.loadPresets()
	a.presetsBox.Objects = nil

	for _, p := range presets {
		value := p
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

// ==== Audio routing ====

// loadAudioState สร้าง sink ถ้ายังไม่มี แล้วดึงข้อมูล obsIndex + stream ทั้งหมด
func (a *App) loadAudioState() (int, []SinkInput, error) {
	if err := ensureSink(); err != nil {
		return -1, nil, fmt.Errorf("สร้าง sink ไม่สำเร็จ: %w", err)
	}
	obsIndex, err := getSinkIndex(sinkName)
	if err != nil {
		return -1, nil, fmt.Errorf("หา sink ไม่เจอ: %w", err)
	}
	inputs, err := listSinkInputs()
	if err != nil {
		return -1, nil, fmt.Errorf("อ่าน audio stream ผิดพลาด: %w", err)
	}
	return obsIndex, inputs, nil
}

// renderSources สร้างปุ่ม toggle ให้แต่ละ stream ตามข้อมูลที่มี
func (a *App) renderSources(obsIndex int, inputs []SinkInput) {
	fyne.Do(func() {
		a.sourcesBox.Objects = nil
		if len(inputs) == 0 {
			a.sourcesBox.Add(widget.NewLabel("ไม่พบแหล่งเสียงที่กำลังเล่นอยู่ตอนนี้"))
		}
		for _, si := range inputs {
			si := si // capture ตัวแปรสำหรับ closure
			connected := si.SinkIndex == obsIndex
			prefix := "⬜ "
			importance := widget.MediumImportance
			if connected {
				prefix = "✅ "
				importance = widget.SuccessImportance
			}
			btnLabel := fmt.Sprintf("%s%s (#%d)", prefix, si.DisplayLabel(), si.ID)

			btn := widget.NewButton(btnLabel, nil)
			btn.Importance = importance
			btn.OnTapped = func() {
				target := sinkName
				if connected {
					target = "@DEFAULT_SINK@"
				}
				if err := moveSinkInput(si.ID, target); err != nil {
					a.appendLog("ย้าย stream #%d ไม่สำเร็จ: %v", si.ID, err)
					return
				}
				a.scan()
			}
			a.sourcesBox.Add(btn)
		}
		a.sourcesBox.Refresh()
	})
}

// scan ดึงรายชื่อ audio stream ทั้งหมดตอนนี้ แล้วสร้างปุ่ม toggle ให้แต่ละตัว
func (a *App) scan() {
	obsIndex, inputs, err := a.loadAudioState()
	if err != nil {
		a.appendLog("สแกนล้มเหลว: %v", err)
		return
	}
	a.renderSources(obsIndex, inputs)
	a.appendLog("สแกนแล้ว เจอ %d แหล่งเสียง", len(inputs))
}

// autoConnectFromPiP เช็คว่ามีหน้าต่าง Picture-in-Picture เปิดอยู่ไหม
// ถ้ามี จะตัดการเชื่อมต่อเดิมทั้งหมด แล้วเชื่อมเฉพาะ stream ของแท็บนั้น
// (แค่อันเดียว) เข้า OBS_Record ให้อัตโนมัติ
func (a *App) autoConnectFromPiP() {
	pid, found, err := findPiPWindowPID()
	if err != nil {
		a.appendLog("เช็คหน้าต่าง PiP ผิดพลาด: %v", err)
		return
	}
	if !found {
		a.appendLog("ไม่พบหน้าต่าง Picture-in-Picture ที่เปิดอยู่ตอนนี้")
		return
	}

	obsIndex, inputs, err := a.loadAudioState()
	if err != nil {
		a.appendLog("%v", err)
		return
	}

	// ตัดการเชื่อมต่อเดิมทั้งหมดก่อน ให้เหลือแค่อันเดียวตามที่ต้องการ
	for _, si := range inputs {
		if si.SinkIndex == obsIndex {
			_ = moveSinkInput(si.ID, "@DEFAULT_SINK@")
		}
	}

	// ลอง match ด้วย PID ตรงๆ ก่อน (แม่นสุดถ้าตรง)
	pidStr := strconv.Itoa(pid)
	var target *SinkInput
	for i := range inputs {
		if inputs[i].Props["application.process.id"] == pidStr {
			target = &inputs[i]
			break
		}
	}

	usedFallback := false
	if target == nil {
		usedFallback = true
		for i := range inputs {
			if !strings.Contains(strings.ToLower(inputs[i].Props["application.name"]), "firefox") {
				continue
			}
			if target == nil || inputs[i].ID > target.ID {
				target = &inputs[i]
			}
		}
	}

	if target == nil {
		a.appendLog("เจอ PiP (pid=%d) แต่หา audio stream ของ Firefox ไม่เจอเลย", pid)
		obsIndex2, inputs2, _ := a.loadAudioState()
		a.renderSources(obsIndex2, inputs2)
		return
	}

	if err := moveSinkInput(target.ID, sinkName); err != nil {
		a.appendLog("เชื่อม stream #%d ไม่สำเร็จ: %v", target.ID, err)
		return
	}

	obsIndex2, inputs2, err := a.loadAudioState()
	if err == nil {
		a.renderSources(obsIndex2, inputs2)
	}

	if usedFallback {
		a.appendLog("⚠️ PID ไม่ตรงกัน (PiP pid=%d) ใช้ fallback เชื่อม stream ล่าสุดแทน: #%d — เช็คด้วยรายการด้านล่างว่าใช่ตัวที่ต้องการไหม", pid, target.ID)
	} else {
		a.appendLog("✅ เจอ PiP (pid=%d) ตรงกับ stream #%d เชื่อมเรียบร้อยแล้ว", pid, target.ID)
	}
}

// disconnectAllFromOBS ย้าย stream ที่เชื่อมกับ OBS อยู่ตอนนี้ทั้งหมดกลับ default sink
func (a *App) disconnectAllFromOBS() {
	obsIndex, inputs, err := a.loadAudioState()
	if err != nil {
		a.appendLog("%v", err)
		return
	}
	count := 0
	for _, si := range inputs {
		if si.SinkIndex != obsIndex {
			continue
		}
		if err := moveSinkInput(si.ID, "@DEFAULT_SINK@"); err != nil {
			a.appendLog("ย้าย stream #%d กลับไม่สำเร็จ: %v", si.ID, err)
			continue
		}
		count++
	}
	if count > 0 {
		a.appendLog("ย้าย %d stream กลับ default sink แล้ว", count)
	}
	obsIndex2, inputs2, err := a.loadAudioState()
	if err == nil {
		a.renderSources(obsIndex2, inputs2)
	}
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
		}
	}
	// ย้าย audio stream ที่เชื่อมกับ OBS อยู่กลับ default sink เสมอไม่ว่าจะหยุดด้วยเหตุผลไหน
	a.disconnectAllFromOBS()

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
