// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
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

	approvedMu  sync.Mutex
	approvedIDs map[int]bool // stream ที่ "ได้รับอนุญาต" ให้อยู่ใน OBS_Record (คนกดเชื่อมเอง)
	guardCancel context.CancelFunc
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

	w.Resize(fyne.NewSize(420, 600))
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
		scanBtn,
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

			toggleBtn := widget.NewButton(btnLabel, nil)
			toggleBtn.Importance = importance
			toggleBtn.OnTapped = func() {
				target := sinkName
				if connected {
					target = "@DEFAULT_SINK@"
				}
				if err := moveSinkInput(si.ID, target); err != nil {
					a.appendLog("ย้าย stream #%d ไม่สำเร็จ: %v", si.ID, err)
					return
				}
				if connected {
					a.unapprove(si.ID)
				} else {
					a.approve(si.ID)
				}
				a.scan()
			}
			a.sourcesBox.Add(toggleBtn)
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

// ==== Guard: ดีด stream ที่ไม่ได้รับอนุญาตออกจาก OBS_Record ====
// ป้องกันกรณีระบบเสียง (PipeWire stream-restore) auto-route stream ใหม่ของ Firefox
// เข้า OBS_Record เองโดยที่เราไม่ได้สั่ง

func (a *App) approve(id int) {
	a.approvedMu.Lock()
	if a.approvedIDs == nil {
		a.approvedIDs = map[int]bool{}
	}
	a.approvedIDs[id] = true
	a.approvedMu.Unlock()
}

func (a *App) unapprove(id int) {
	a.approvedMu.Lock()
	delete(a.approvedIDs, id)
	a.approvedMu.Unlock()
}

func (a *App) isApproved(id int) bool {
	a.approvedMu.Lock()
	defer a.approvedMu.Unlock()
	return a.approvedIDs[id]
}

func (a *App) clearApproved() {
	a.approvedMu.Lock()
	a.approvedIDs = map[int]bool{}
	a.approvedMu.Unlock()
}

// startOBSGuard เริ่ม loop เฝ้าตลอดช่วงอัด คอยเช็คว่ามี stream แปลกปลอมที่ไม่ได้รับ
// อนุญาตหลุดเข้า OBS_Record มาเองมั้ย (เช่นจาก PipeWire auto-restore) ถ้าเจอจะดีดออกทันที
func (a *App) startOBSGuard() {
	ctx, cancel := context.WithCancel(context.Background())
	a.guardCancel = cancel
	go func() {
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.guardTick()
			}
		}
	}()
}

func (a *App) stopOBSGuard() {
	if a.guardCancel != nil {
		a.guardCancel()
		a.guardCancel = nil
	}
}

func (a *App) guardTick() {
	obsIndex, inputs, err := a.loadAudioState()
	if err != nil {
		return
	}
	changed := false
	for _, si := range inputs {
		if si.SinkIndex != obsIndex {
			continue
		}
		if a.isApproved(si.ID) {
			continue
		}
		if err := moveSinkInput(si.ID, "@DEFAULT_SINK@"); err != nil {
			a.appendLog("ดีด stream #%d ที่ไม่ได้รับอนุญาตออกไม่สำเร็จ: %v", si.ID, err)
			continue
		}
		a.appendLog("🚫 มีเสียงที่ไม่ได้รับอนุญาต (#%d %s) หลุดเข้า OBS มาเอง ดีดออกกลับ default sink แล้ว", si.ID, si.DisplayLabel())
		changed = true
	}
	if changed {
		obsIndex2, inputs2, err := a.loadAudioState()
		if err == nil {
			a.renderSources(obsIndex2, inputs2)
		}
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

	a.startOBSGuard()

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
	a.stopOBSGuard()
	a.clearApproved()
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
