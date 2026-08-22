// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"context"
	_ "embed"
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
	"github.com/zalando/go-keyring"
)

const (
	presetsPrefKey  = "saved_times"
	hostPrefKey     = "obs_host"
	passwordPrefKey = "obs_password" // legacy key; migrated to the OS keyring
	keyringService  = "com.pip-recorder.app"
	keyringUser     = "obs-websocket-password"

	defaultOBSHost = "localhost:4455"
)

type App struct {
	fyneApp            fyne.App
	window             fyne.Window
	obs                *OBSController
	passwordStorageErr error

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
	stateMu       sync.Mutex

	audioCancel context.CancelFunc // ตัวเดียวคุมทั้ง guard + pipWatch (ใช้ subscribe ร่วมกัน)
	pipMatched  bool               // true = เจอและเชื่อม PiP ของรอบนี้แล้ว ห้ามเชื่อมอะไรอัตโนมัติอีกจนกว่าจะกด Record รอบใหม่

	approvedMu  sync.Mutex
	approvedIDs map[int]bool // stream ที่ "ได้รับอนุญาต" ให้อยู่ใน OBS_Record (คนกดเชื่อมเอง)
}

//go:embed assets/font/Itim-Regular.ttf
var fontItim []byte
var myFont = fyne.NewStaticResource("Itim-Regular.ttf", fontItim)

func main() {
	a := app.NewWithID("com.nawakarit.obsRec")
	a.Settings().SetTheme(&MyTheme{})

	w := a.NewWindow("PiP Recorder")

	myApp := &App{
		fyneApp: a,
		window:  w,
	}
	myApp.buildUI()
	myApp.tryAutoConnect()

	w.Resize(fyne.NewSize(420, 780))
	w.ShowAndRun()
}

func (a *App) buildUI() {
	// ==== ส่วนเชื่อมต่อ OBS ====
	savedHost := a.fyneApp.Preferences().StringWithFallback(hostPrefKey, defaultOBSHost)
	savedPassword, passwordErr := loadOBSPassword(a.fyneApp.Preferences())
	a.passwordStorageErr = passwordErr

	a.hostEntry = widget.NewEntry()
	a.hostEntry.SetText(savedHost)
	a.hostEntry.SetPlaceHolder("localhost:4455")

	a.passwordEntry = widget.NewPasswordEntry()
	a.passwordEntry.SetText(savedPassword)
	a.passwordEntry.SetPlaceHolder("OBS WebSocket password")

	a.connStatus = widget.NewLabel("ยังไม่ได้เชื่อมต่อ OBS")
	if passwordErr != nil {
		a.connStatus.SetText("ระบบ keyring ใช้งานไม่ได้ กรุณาตรวจสอบก่อนเชื่อมต่อ")
	}
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
	a.logBox.SetMinRowsVisible(11)

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
		a.logBox,
	)
	a.window.SetContent(container.NewScroll(content))

	a.refreshPresets()
}

func (a *App) appendLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fyne.Do(func() {
		a.logBox.SetText(a.logBox.Text + msg + "\n")
	})
}

func loadOBSPassword(preferences fyne.Preferences) (string, error) {
	password, err := keyring.Get(keyringService, keyringUser)
	if err == nil {
		preferences.RemoveValue(passwordPrefKey)
		return password, nil
	}
	if err != keyring.ErrNotFound {
		return "", fmt.Errorf("อ่าน OBS password จากระบบ keyring ไม่สำเร็จ: %w", err)
	}

	// Migrate passwords saved by older versions, then remove the plaintext value.
	legacyPassword := preferences.String(passwordPrefKey)
	if legacyPassword == "" {
		return "", nil
	}
	if err := keyring.Set(keyringService, keyringUser, legacyPassword); err != nil {
		return "", fmt.Errorf("ย้าย OBS password ไปยังระบบ keyring ไม่สำเร็จ: %w", err)
	}
	preferences.RemoveValue(passwordPrefKey)
	return legacyPassword, nil
}

// ==== เชื่อมต่อ OBS ====

func (a *App) tryAutoConnect() {
	if a.passwordStorageErr != nil {
		return
	}
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

	// เก็บเฉพาะ host ใน Fyne preferences; password อยู่ใน OS keyring เท่านั้น
	a.fyneApp.Preferences().SetString(hostPrefKey, host)
	if err := keyring.Set(keyringService, keyringUser, password); err != nil {
		a.obs.Close()
		a.obs = nil
		a.connStatus.SetText("เชื่อมต่อสำเร็จ แต่บันทึกรหัสผ่านไม่สำเร็จ")
		dialog.ShowError(fmt.Errorf("เชื่อมต่อ OBS สำเร็จ แต่บันทึก password ในระบบ keyring ไม่สำเร็จ: %w", err), a.window)
		return
	}
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

// ==== ตรวจจับเสียงแรกหลังกด Record (baseline-diff auto-connect) ====
// Part 4: รวมเข้ากับ guard ให้ใช้ pactl subscribe ตัวเดียวกัน ไม่ต้องเปิด
// 2 process แยกกันแล้ว ดูฟังก์ชัน startAudioMonitoring ด้านล่าง

// pipWatchTick เช็ครอบเดียว คืนค่า true ถ้าเชื่อมสำเร็จแล้ว (ให้เลิก watch ต่อได้)

func (a *App) pipWatchTick(baseline map[int]bool) bool {
	a.stateMu.Lock()
	pipMatched := a.pipMatched
	a.stateMu.Unlock()
	if pipMatched {
		return true
	}

	// เช็คก่อนว่ามีหน้าต่าง PiP เปิดอยู่จริงตอนนี้มั้ย ถ้าไม่มี ไม่ต้องพิจารณาอะไรเลย
	// (กันเผลอเปิดวิดีโออื่นที่ไม่ใช่ PiP แล้วโดนดึงเข้ามาผิดตัว)
	_, pipOpen, err := findPiPWindowPID()
	if err != nil {
		a.appendLog("เช็คหน้าต่าง PiP ผิดพลาด: %v", err)
		return false
	}
	if !pipOpen {
		return false
	}

	obsIndex, inputs, err := a.loadAudioState()
	if err != nil {
		a.appendLog("auto-watch อ่านสถานะไม่สำเร็จ: %v", err)
		return false
	}

	var candidates []SinkInput
	for _, si := range inputs {
		if baseline[si.ID] {
			continue // มีอยู่ก่อนกด Record แล้ว ไม่ใช่ตัวใหม่ที่ต้องการ
		}
		if si.SinkIndex == obsIndex {
			continue // เชื่อมอยู่แล้ว
		}
		if si.Corked {
			continue // ยังไม่มีเสียงออกจริงตอนนี้
		}
		if !strings.Contains(strings.ToLower(si.Props["application.name"]), "firefox") {
			continue
		}
		candidates = append(candidates, si)
	}

	switch len(candidates) {
	case 0:
		return false
	case 1:
		target := candidates[0]
		if err := moveSinkInput(target.ID, sinkName); err != nil {
			a.appendLog("เชื่อม stream #%d ไม่สำเร็จ: %v", target.ID, err)
			return false
		}
		a.appendLog("✅ ออโต้จับ PiP สำเร็จ: stream ใหม่ #%d (%s) เชื่อมกับ OBS แล้ว", target.ID, target.DisplayLabel())
		a.approve(target.ID)
		a.stateMu.Lock()
		a.pipMatched = true
		a.stateMu.Unlock()
		a.appendLog("🔒 หยุดเฝ้าดูอัตโนมัติแล้ว หลังจากนี้จนกว่าจะกด Record รอบใหม่ จะไม่เชื่อมอะไรเพิ่มเองอีกเด็ดขาด (เชื่อมเองได้ผ่านรายการด้านล่างเท่านั้น)")
		obsIndex2, inputs2, err := a.loadAudioState()
		if err == nil {
			a.renderSources(obsIndex2, inputs2)
		}
		return true
	default:
		a.appendLog("เจอ stream ใหม่มากกว่า 1 ตัวพร้อมกัน (%d ตัว) ไม่แน่ใจว่าตัวไหนคือ PiP รอดูรอบถัดไปหรือเลือกเองด้านล่าง", len(candidates))
		return false
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

// startAudioMonitoring เริ่มเฝ้าตลอดช่วงอัด — รวม guard (ดีด stream ที่ไม่ได้รับ
// อนุญาต) กับ pipWatch (จับ PiP ตัวแรกอัตโนมัติ) เข้าด้วยกัน ใช้ pactl subscribe
// ตัวเดียว (Part 4) แทนที่จะเปิด 2 process แยกกันเหมือนก่อนหน้านี้ ลดภาระระบบลง
func (a *App) startAudioMonitoring() {
	// จด baseline ของ stream ที่มีอยู่ก่อนกด Record ไว้ก่อน (ไว้ให้ pipWatch ใช้แยกว่า
	// stream ไหน "ใหม่" หลังจากนี้ ไม่ไปยุ่งกับตัวที่เล่นอยู่ก่อนแล้ว)
	_, inputs, err := a.loadAudioState()
	if err != nil {
		a.appendLog("เริ่ม audio monitoring ไม่สำเร็จ: %v", err)
		return
	}
	baseline := make(map[int]bool, len(inputs))
	for _, si := range inputs {
		baseline[si.ID] = true
	}
	a.stateMu.Lock()
	a.pipMatched = false
	a.stateMu.Unlock()
	a.appendLog("เริ่มเฝ้าเสียง (มี %d stream อยู่ก่อนแล้ว จะไม่แตะต้อง)", len(baseline))

	ctx, cancel := context.WithCancel(context.Background())
	a.stateMu.Lock()
	a.audioCancel = cancel
	a.stateMu.Unlock()

	dispatch := func() {
		a.guardTick()
		a.stateMu.Lock()
		pipMatched := a.pipMatched
		a.stateMu.Unlock()
		if !pipMatched {
			a.pipWatchTick(baseline)
		}
	}

	events, err := subscribeSinkInputEvents(ctx)
	if err != nil {
		a.appendLog("audio monitoring: เปิดฟัง event ไม่สำเร็จ จะใช้ fallback ticker อย่างเดียว: %v", err)
	} else {
		// Part 5: debounce — ถ้า event มาถี่ๆ ติดกัน (เช่นหลาย stream เปลี่ยนพร้อมกัน)
		// จะยุบเหลือ dispatch แค่ครั้งเดียวหลังเงียบไป 200ms แทนที่จะยิง pactl list
		// รัวๆ ทุก event เพิ่ม latency นิดหน่อย (สูงสุด 200ms) แลกกับ CPU ที่ประหยัดกว่ามาก
		go func() {
			var debounce *time.Timer
			for range events {
				if debounce == nil {
					debounce = time.AfterFunc(200*time.Millisecond, dispatch)
				} else {
					debounce.Reset(200 * time.Millisecond)
				}
			}
		}()
	}

	// fallback ticker ความถี่ต่ำ กันพลาดกรณี event หลุดหรือ subscribe ตายกลางคัน
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dispatch()
			}
		}
	}()

	// เช็ครอบแรกทันทีตอนเริ่ม เผื่อมีอะไรค้างอยู่ก่อนแล้ว
	dispatch()
}

func (a *App) stopAudioMonitoring() {
	a.stateMu.Lock()
	cancel := a.audioCancel
	a.audioCancel = nil
	a.stateMu.Unlock()
	if cancel != nil {
		cancel()
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
	a.stateMu.Lock()
	if a.recording {
		a.stateMu.Unlock()
		return
	}
	a.stateMu.Unlock()
	dur, err := parseMMSS(a.timeEntry.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("กรอกเวลาไม่ถูกต้อง (ใช้รูปแบบ mm:ss): %w", err), a.window)
		return
	}
	if a.obs == nil {
		dialog.ShowError(fmt.Errorf("ยังไม่ได้เชื่อมต่อ OBS สำเร็จ (กด \"เชื่อมต่อ OBS\" ก่อน)"), a.window)
		return
	}

	stopRequested := make(chan struct{})
	a.stateMu.Lock()
	if a.recording {
		a.stateMu.Unlock()
		return
	}
	a.recording = true
	a.stopRequested = stopRequested
	a.stateMu.Unlock()

	if err := a.obs.StartRecord(); err != nil {
		a.stateMu.Lock()
		a.recording = false
		a.stopRequested = nil
		a.stateMu.Unlock()
		dialog.ShowError(err, a.window)
		return
	}

	a.recordBtn.Disable()
	a.stopBtn.Enable()
	a.setStatus("เริ่มอัดแล้ว กำลังนับเวลา...")

	a.startAudioMonitoring()

	go a.runRecordingFlow(dur, stopRequested)
}

func (a *App) onStopPressed() {
	a.stateMu.Lock()
	if !a.recording || a.stopRequested == nil {
		a.stateMu.Unlock()
		return
	}
	stopRequested := a.stopRequested
	a.stopRequested = nil
	a.stateMu.Unlock()
	close(stopRequested)
}

func (a *App) runRecordingFlow(timerDur time.Duration, stopRequested <-chan struct{}) {
	if a.waitTimer(timerDur, stopRequested) {
		a.finishRecording("ครบเวลาแล้ว หยุดอัดอัตโนมัติ")
	}
}

func (a *App) waitTimer(dur time.Duration, stopRequested <-chan struct{}) bool {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	remaining := dur

	for remaining > 0 {
		select {
		case <-stopRequested:
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
	a.stateMu.Lock()
	if !a.recording {
		a.stateMu.Unlock()
		return
	}
	a.recording = false
	a.stopRequested = nil
	a.stateMu.Unlock()

	if a.obs != nil {
		if err := a.obs.StopRecord(); err != nil {
			a.setStatus(fmt.Sprintf("หยุดอัดผิดพลาด: %v", err))
		}
	}
	// ย้าย audio stream ที่เชื่อมกับ OBS อยู่กลับ default sink เสมอไม่ว่าจะหยุดด้วยเหตุผลไหน
	a.stopAudioMonitoring()
	a.stateMu.Lock()
	a.pipMatched = false
	a.stateMu.Unlock()
	a.clearApproved()
	a.disconnectAllFromOBS()

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
