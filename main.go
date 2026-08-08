// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
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

type App struct {
	fyneApp fyne.App
	window  fyne.Window
	obs     *OBSController

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

	w.Resize(fyne.NewSize(340, 200))
	w.ShowAndRun()
}

func (a *App) buildUI() {
	a.timeEntry = widget.NewEntry()
	a.timeEntry.SetPlaceHolder("mm:ss เช่น 10:30")

	a.statusLabel = widget.NewLabel("พร้อมทำงาน")
	a.statusLabel.Wrapping = fyne.TextWrapWord

	a.recordBtn = widget.NewButton("Record", a.onRecordPressed)
	a.stopBtn = widget.NewButton("หยุดเอง", a.onStopPressed)
	a.stopBtn.Disable()

	content := container.NewVBox(
		widget.NewLabel("เวลาโดยประมาณ (mm:ss):"),
		a.timeEntry,
		container.NewHBox(a.recordBtn, a.stopBtn),
		widget.NewSeparator(),
		a.statusLabel,
	)
	a.window.SetContent(content)
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
