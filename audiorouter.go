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
	MediaName string // มักเป็นชื่อหน้า/สื่อที่กำลังเล่น ช่วยให้คนแยกได้ว่าเป็นแท็บไหน
	Corked    bool   // true = หยุดเล่นชั่วคราว (paused/ไม่มีเสียงออกตอนนี้)
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
var mediaNameRe = regexp.MustCompile(`media\.name = "([^"]*)"`)
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
		if mm := mediaNameRe.FindStringSubmatch(block); mm != nil {
			si.MediaName = mm[1]
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
	routed map[int]string // sink-input id -> label ที่ถูกย้ายไปแล้ว
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
	r.routed = make(map[int]string)
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
	r.routed = make(map[int]string)
	r.mu.Unlock()
}

// ListPlayingFirefoxStreams คืนรายการ audio stream ของ Firefox ที่กำลังเล่นอยู่จริง
// (ไม่ corked) และยังไม่เคยถูก route ไว้ก่อน ให้ UI เอาไปแสดงให้คนเลือกเอง
func (r *AudioRouter) ListPlayingFirefoxStreams() ([]SinkInput, error) {
	inputs, err := listSinkInputs()
	if err != nil {
		return nil, err
	}
	var result []SinkInput
	for _, si := range inputs {
		if !strings.Contains(strings.ToLower(si.AppName), "firefox") {
			continue
		}
		if si.Corked {
			continue
		}
		if r.IsRouted(si.ID) {
			continue
		}
		result = append(result, si)
	}
	return result, nil
}

// IsRouted เช็คว่า sink-input id นี้ถูกย้ายเข้า OBS sink ไปแล้วหรือยัง
func (r *AudioRouter) IsRouted(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.routed[id]
	return ok
}

// ListRouted คืนรายการ (id, label) ของ stream ที่กำลังเชื่อมกับ OBS อยู่ตอนนี้
func (r *AudioRouter) ListRouted() []SinkInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]SinkInput, 0, len(r.routed))
	for id, label := range r.routed {
		result = append(result, SinkInput{ID: id, MediaName: label})
	}
	return result
}

// RouteManually ย้าย sink-input ที่ระบุเข้า OBS sink ตามที่คนเลือกเองจาก UI
func (r *AudioRouter) RouteManually(id int, label string) error {
	if err := moveSinkInput(id, sinkName); err != nil {
		return fmt.Errorf("ย้าย stream ไม่สำเร็จ: %w", err)
	}
	r.mu.Lock()
	if r.routed == nil {
		r.routed = make(map[int]string)
	}
	r.routed[id] = label
	r.mu.Unlock()
	r.logf("เชื่อมเอง: ย้าย stream #%d (%s) เข้า %s แล้ว", id, label, sinkName)
	return nil
}

// DisconnectOne ยกเลิกการเชื่อม stream เดียว ย้ายกลับ default sink
func (r *AudioRouter) DisconnectOne(id int) error {
	if err := moveSinkInput(id, "@DEFAULT_SINK@"); err != nil {
		return fmt.Errorf("ย้าย stream กลับไม่สำเร็จ: %w", err)
	}
	r.mu.Lock()
	label := r.routed[id]
	delete(r.routed, id)
	r.mu.Unlock()
	r.logf("ยกเลิกเอง: ย้าย stream #%d (%s) กลับ default sink แล้ว", id, label)
	return nil
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
		_, already := r.routed[si.ID]
		r.mu.Unlock()
		if already {
			continue
		}
		if err := moveSinkInput(si.ID, sinkName); err != nil {
			r.logf("ย้าย stream #%d ไม่สำเร็จ: %v", si.ID, err)
			continue
		}
		label := si.MediaName
		if label == "" {
			label = si.AppName
		}
		r.mu.Lock()
		r.routed[si.ID] = label
		r.mu.Unlock()
		r.logf("เจอ PiP (PID %d) -> ย้าย stream #%d (%s) เข้า %s แล้ว", pid, si.ID, si.AppName, sinkName)
	}

	if matched {
		return
	}

	// ไม่เจอ process.id ตรงกันเป๊ะ -> ไม่เดาต่อแล้ว (เดามั่วมาก่อนหน้านี้ทำให้ดึงเสียง
	// จาก Firefox หน้าต่างอื่นมาผิดๆ) ปล่อยให้คนเลือกเองจากรายการ "เสียงที่กำลังเล่นอยู่"
	// ในหน้า UI แทน ผ่าน RouteManually()
}
