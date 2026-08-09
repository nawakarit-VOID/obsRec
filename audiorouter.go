// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ชื่อ virtual sink ที่ OBS จะ capture เสียงจาก monitor ของมัน
const sinkName = "OBS_Record"

// SinkInput เก็บข้อมูล audio stream หนึ่งตัวจาก `pactl list sink-inputs`
type SinkInput struct {
	ID        int
	ProcessID int
	AppName   string
	Corked    bool // true = หยุดเล่นชั่วคราว (paused/ไม่มีเสียงออกตอนนี้)
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sinkExists ตรวจสอบว่า virtual sink มีอยู่แล้วหรือยัง
func sinkExists() bool {
	out, err := runCmd("pactl", "list", "sinks", "short")
	if err != nil {
		return false
	}
	return strings.Contains(out, sinkName)
}

// ensureSink สร้าง null-sink ถ้ายังไม่มี (idempotent)
func ensureSink() error {
	if sinkExists() {
		return nil
	}
	_, err := runCmd("pactl", "load-module", "module-null-sink",
		fmt.Sprintf("sink_name=%s", sinkName),
		fmt.Sprintf("sink_properties=device.description=%s", sinkName))
	return err
}

// รูปแบบบรรทัดของ `wmctrl -lx`:
// <window_id> <desktop> <WM_CLASS> <hostname> <title...>
var wmctrlLineRe = regexp.MustCompile(`^(\S+)\s+(-?\d+)\s+(\S+)\s+(\S+)\s+(.*)$`)

// findPiPWindowPID มองหาหน้าต่าง Picture-in-Picture ของ Firefox
// สังเกตจาก WM_CLASS ที่ขึ้นต้นด้วย "Toolkit" (คงที่ไม่ว่า UI จะภาษาอะไร)
// แล้วคืน PID ของหน้าต่างนั้น
func findPiPWindowPID() (int, bool, error) {
	out, err := runCmd("wmctrl", "-lx")
	if err != nil {
		return 0, false, err
	}
	for _, line := range strings.Split(out, "\n") {
		m := wmctrlLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		windowID, class := m[1], m[3]
		if !strings.Contains(strings.ToLower(class), "toolkit") {
			continue
		}
		pidOut, err := runCmd("xdotool", "getwindowpid", windowID)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidOut))
		if err != nil {
			continue
		}
		return pid, true, nil
	}
	return 0, false, nil
}

var sinkInputHeaderRe = regexp.MustCompile(`(?m)^Sink Input #(\d+)`)
var processIDRe = regexp.MustCompile(`application\.process\.id = "(\d+)"`)
var appNameRe = regexp.MustCompile(`application\.name = "([^"]*)"`)
var corkedRe = regexp.MustCompile(`Corked:\s*(yes|no)`)

// listSinkInputs อ่าน audio stream ทั้งหมดที่กำลังเล่นอยู่ตอนนี้
func listSinkInputs() ([]SinkInput, error) {
	out, err := runCmd("pactl", "list", "sink-inputs")
	if err != nil {
		return nil, err
	}
	idxs := sinkInputHeaderRe.FindAllStringSubmatchIndex(out, -1)
	var result []SinkInput
	for i, loc := range idxs {
		start := loc[0]
		end := len(out)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := out[start:end]

		id, _ := strconv.Atoi(out[loc[2]:loc[3]])
		si := SinkInput{ID: id}
		if pm := processIDRe.FindStringSubmatch(block); pm != nil {
			si.ProcessID, _ = strconv.Atoi(pm[1])
		}
		if am := appNameRe.FindStringSubmatch(block); am != nil {
			si.AppName = am[1]
		}
		if cm := corkedRe.FindStringSubmatch(block); cm != nil {
			si.Corked = cm[1] == "yes"
		}
		result = append(result, si)
	}
	return result, nil
}

// moveSinkInput ย้าย audio stream ไปยัง sink ปลายทาง
func moveSinkInput(id int, sink string) error {
	_, err := runCmd("pactl", "move-sink-input", strconv.Itoa(id), sink)
	return err
}

// AudioRouter เฝ้าหาหน้าต่าง PiP ของ Firefox แล้วย้าย audio stream ที่ตรงกันเข้า
// sinkName โดยอัตโนมัติ เพื่อแทนที่ขั้นตอนลากสายเสียงมือใน qpwgraph
type AudioRouter struct {
	mu     sync.Mutex
	routed map[int]bool // sink-input id ที่ถูกย้ายไปแล้ว กันย้ำซ้ำ
	cancel context.CancelFunc
	logf   func(format string, args ...interface{})
}

func NewAudioRouter(logf func(format string, args ...interface{})) *AudioRouter {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &AudioRouter{logf: logf}
}

// Start สร้าง sink (ถ้ายังไม่มี) แล้วเริ่มเฝ้าหน้าต่าง PiP ในพื้นหลัง
func (r *AudioRouter) Start() error {
	if err := ensureSink(); err != nil {
		return fmt.Errorf("สร้าง sink ไม่สำเร็จ: %w", err)
	}
	r.logf("สร้าง/ยืนยัน sink %s เรียบร้อย", sinkName)

	r.mu.Lock()
	r.routed = make(map[int]bool)
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.watchLoop(ctx)
	return nil
}

// Stop เลิกเฝ้า และย้าย stream ที่เคยย้ายไว้กลับ default sink ทั้งหมด
func (r *AudioRouter) Stop() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.mu.Lock()
	for id := range r.routed {
		if err := moveSinkInput(id, "@DEFAULT_SINK@"); err != nil {
			r.logf("ย้าย stream #%d กลับไม่สำเร็จ: %v", id, err)
		} else {
			r.logf("ย้าย stream #%d กลับ default sink แล้ว", id)
		}
	}
	r.routed = make(map[int]bool)
	r.mu.Unlock()
}

func (r *AudioRouter) watchLoop(ctx context.Context) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

func (r *AudioRouter) tick() {
	pid, found, err := findPiPWindowPID()
	if err != nil {
		r.logf("เช็คหน้าต่าง PiP ผิดพลาด: %v", err)
		return
	}
	if !found {
		return
	}

	inputs, err := listSinkInputs()
	if err != nil {
		r.logf("อ่าน audio stream ผิดพลาด: %v", err)
		return
	}

	matched := false
	for _, si := range inputs {
		if si.ProcessID != pid {
			continue
		}
		matched = true
		r.mu.Lock()
		already := r.routed[si.ID]
		r.mu.Unlock()
		if already {
			continue
		}
		if err := moveSinkInput(si.ID, sinkName); err != nil {
			r.logf("ย้าย stream #%d ไม่สำเร็จ: %v", si.ID, err)
			continue
		}
		r.mu.Lock()
		r.routed[si.ID] = true
		r.mu.Unlock()
		r.logf("เจอ PiP (PID %d) -> ย้าย stream #%d (%s) เข้า %s แล้ว", pid, si.ID, si.AppName, sinkName)
	}

	if matched {
		return
	}

	// Fallback: ไม่เจอ process.id ตรงกันเป๊ะ (Firefox สมัยใหม่ PID ของหน้าต่าง PiP
	// มักเป็น main process ไม่ใช่ content process ที่เล่นเสียงจริง เลย match ตรงๆ ไม่ค่อยติด)
	//
	// เกณฑ์ที่ใช้แทน: เอาเฉพาะ stream ของ Firefox ที่ "กำลังเล่นเสียงอยู่จริงตอนนี้"
	// (Corked = no) และยังไม่เคยถูกย้าย ถ้าเจอตัวเดียวถือว่าใช่แน่ ย้ายเลย
	// ถ้าเจอมากกว่า 1 ตัวพร้อมกัน (มีแท็บอื่นเล่นเสียงคาไว้ด้วย) จะไม่เดา แค่ log เตือนไว้
	// กันย้ายผิดตัว รอบถัดไปค่อยลองใหม่
	var candidates []SinkInput
	for _, si := range inputs {
		if !strings.Contains(strings.ToLower(si.AppName), "firefox") {
			continue
		}
		if si.Corked {
			continue // ไม่ได้เล่นเสียงอยู่ตอนนี้ ข้ามไป ไม่ใช่ตัวที่ต้องการแน่ๆ
		}
		r.mu.Lock()
		already := r.routed[si.ID]
		r.mu.Unlock()
		if !already {
			candidates = append(candidates, si)
		}
	}

	switch len(candidates) {
	case 0:
		return
	case 1:
		fallback := candidates[0]
		if err := moveSinkInput(fallback.ID, sinkName); err != nil {
			r.logf("ย้าย stream fallback ไม่สำเร็จ: %v", err)
			return
		}
		r.mu.Lock()
		r.routed[fallback.ID] = true
		r.mu.Unlock()
		r.logf("ไม่พบ PID ตรงกัน ใช้ fallback (เล่นอยู่จริง): ย้าย stream #%d (%s) เข้า %s", fallback.ID, fallback.AppName, sinkName)
	default:
		r.logf("เจอ Firefox stream ที่กำลังเล่นอยู่หลายตัวพร้อมกัน (%d ตัว) ไม่แน่ใจว่าตัวไหนคือ PiP เลยข้ามรอบนี้ไปก่อน", len(candidates))
	}
}
