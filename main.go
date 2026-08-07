// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type RecordMode string

const (
	ModeTimerOnly      RecordMode = "timer"
	ModeTimerPlusAudio RecordMode = "timer_audio"
	prefKeyLastMode               = "last_mode"
)

type App struct {
	fyneApp fyne.App
	window  fyne.Window
	obs     *OBSController

	modeSelect  *widget.RadioGroup
	timeEntry   *widget.Entry
	statusLabel *widget.Label
	recordBtn   *widget.Button
	stopBtn     *widget.Button

	stopRequested chan struct{}
	recording     bool
}

func main() {
	a := app.New()
	w := a.NewWindow("PiP Recorder")

	obs, err := NewOBSController(obsHost, obsPassword)
	if err != nil {
		// ยังเปิดหน้าต่างได้ แต่แจ้ง error ให้เห็น เผื่อ OBS ยังไม่เปิด/WebSocket ยังไม่ enable
		dialog.ShowError(err, w)
	}

	myApp := &App{
		fyneApp: a,
		window:  w,
		obs:     obs,
	}
	myApp.buildUI()

	w.Resize(fyne.NewSize(360, 260))
	w.ShowAndRun()
}

func (a *App) buildUI() {
	lastMode := a.fyneApp.Preferences().StringWithFallback(prefKeyLastMode, string(ModeTimerPlusAudio))

	a.modeSelect = widget.NewRadioGroup([]string{
		"Timer เท่านั้น",
		"Timer + ตรวจจับเสียง",
	}, nil)
	if lastMode == string(ModeTimerOnly) {
		a.modeSelect.SetSelected("Timer เท่านั้น")
	} else {
		a.modeSelect.SetSelected("Timer + ตรวจจับเสียง")
	}

	a.timeEntry = widget.NewEntry()
	a.timeEntry.SetPlaceHolder("mm:ss เช่น 10:30")

	a.statusLabel = widget.NewLabel("พร้อมทำงาน")
	a.statusLabel.Wrapping = fyne.TextWrapWord

	a.recordBtn = widget.NewButton("Record", a.onRecordPressed)
	a.stopBtn = widget.NewButton("หยุดเอง", a.onStopPressed)
	a.stopBtn.Disable()

	content := container.NewVBox(
		widget.NewLabel("โหมด:"),
		a.modeSelect,
		widget.NewLabel("เวลาโดยประมาณ (mm:ss):"),
		a.timeEntry,
		container.NewHBox(a.recordBtn, a.stopBtn),
		widget.NewSeparator(),
		a.statusLabel,
	)
	a.window.SetContent(content)
}

func (a *App) currentMode() RecordMode {
	if a.modeSelect.Selected == "Timer เท่านั้น" {
		return ModeTimerOnly
	}
	return ModeTimerPlusAudio
}

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
		dialog.ShowError(fmt.Errorf("ยังไม่ได้เชื่อมต่อ OBS สำเร็จ (เช็ค OBS เปิดอยู่มั้ย และ WebSocket enable แล้วมั้ย)"), a.window)
		return
	}

	mode := a.currentMode()
	a.fyneApp.Preferences().SetString(prefKeyLastMode, string(mode))

	if err := a.obs.StartRecord(); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	a.recording = true
	a.stopRequested = make(chan struct{})
	a.recordBtn.Disable()
	a.stopBtn.Enable()
	a.setStatus("เริ่มอัดแล้ว กำลังนับเวลา...")

	go a.runRecordingFlow(mode, dur)
}

func (a *App) onStopPressed() {
	if !a.recording {
		return
	}
	close(a.stopRequested)
}

// runRecordingFlow คือ state machine หลัก รันใน goroutine แยก
func (a *App) runRecordingFlow(mode RecordMode, timerDur time.Duration) {
	// Phase 1: รอตาม timer ก่อนเสมอ ไม่สนใจเสียงระหว่างนี้
	if !a.waitTimer(timerDur) {
		return // ถูกสั่งหยุดเองระหว่าง timer
	}

	if mode == ModeTimerOnly {
		a.finishRecording("ครบเวลาแล้ว หยุดอัดอัตโนมัติ")
		return
	}

	// Phase 2: พ้น timer แล้ว เริ่มฟังเสียง
	a.setStatus("พ้นเวลาขั้นต่ำแล้ว กำลังฟังเสียง...")
	a.listenForSilenceLoop()
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

// listenForSilenceLoop ฟังเสียงวนไป จนกว่าจะเจอเงียบแล้วคนไม่กดยกเลิก หรือกดหยุดเอง
func (a *App) listenForSilenceLoop() {
	for {
		silenceCh := make(chan struct{}, 1)
		monitorCtx, cancelMonitor := context.WithCancel(context.Background())

		mon := NewAudioMonitor(audioMonitorSource, silenceAmplitudeThreshold, silenceDurationToTrigger)
		go func() {
			_ = mon.Start(monitorCtx, func() {
				select {
				case silenceCh <- struct{}{}:
				default:
				}
			})
		}()

		select {
		case <-a.stopRequested:
			cancelMonitor()
			a.finishRecording("หยุดอัดด้วยตนเอง")
			return

		case <-silenceCh:
			cancelMonitor()
			cancelled := a.showSilenceCountdown(stopCountdownDuration)
			if cancelled {
				a.setStatus("ยกเลิกแล้ว กำลังฟังเสียงต่อ...")
				continue // วนฟังเสียงใหม่
			}
			a.finishRecording("ตรวจพบเงียบ หยุดอัดอัตโนมัติ")
			return
		}
	}
}

// showSilenceCountdown แสดง dialog นับถอยหลัง คืนค่า true ถ้าคนกดยกเลิก (ยังไม่จบ)
// false ถ้าปล่อยให้นับจนครบ (ให้หยุดอัดจริง)
func (a *App) showSilenceCountdown(d time.Duration) bool {
	resultCh := make(chan bool, 1)

	fyne.Do(func() {
		remaining := int(d.Seconds())
		msg := widget.NewLabel(fmt.Sprintf("ตรวจพบเสียงเงียบ จะหยุดอัดใน %d วินาที", remaining))

		var dlg dialog.Dialog
		cancelled := false

		confirmBtn := widget.NewButton("ยังไม่จบ (ยกเลิก)", func() {
			cancelled = true
			dlg.Hide()
		})

		dlg = dialog.NewCustomWithoutButtons("ตรวจพบความเงียบ", container.NewVBox(msg, confirmBtn), a.window)
		dlg.SetOnClosed(func() {
			resultCh <- cancelled
		})
		dlg.Show()

		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			left := remaining
			for left > 0 {
				<-ticker.C
				left--
				l := left
				fyne.Do(func() {
					msg.SetText(fmt.Sprintf("ตรวจพบเสียงเงียบ จะหยุดอัดใน %d วินาที", l))
				})
			}
			fyne.Do(func() {
				dlg.Hide() // ครบเวลา ปิดเอง -> cancelled ยัง false อยู่ -> หยุดอัดจริง
			})
		}()
	})

	return <-resultCh
}

func (a *App) finishRecording(reason string) {
	if a.obs != nil {
		if err := a.obs.StopRecord(); err != nil {
			a.setStatus(fmt.Sprintf("หยุดอัดผิดพลาด: %v", err))
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
