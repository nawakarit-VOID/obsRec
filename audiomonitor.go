// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"time"
)

// AudioMonitor ฟังระดับเสียงจาก pipewire source ที่กำหนด (ผ่าน parec ซึ่งเป็น
// pulseaudio-compat tool ที่ pipewire-pulse รองรับอยู่แล้ว) แล้วเรียก onSilence
// เมื่อเงียบต่อเนื่องเกิน silenceDuration
type AudioMonitor struct {
	source          string
	amplitudeThresh float64
	silenceDuration time.Duration
}

func NewAudioMonitor(source string, amplitudeThresh float64, silenceDuration time.Duration) *AudioMonitor {
	return &AudioMonitor{
		source:          source,
		amplitudeThresh: amplitudeThresh,
		silenceDuration: silenceDuration,
	}
}

// Start เริ่มฟังเสียง จะ block อยู่จนกว่า ctx จะถูก cancel หรือเจอความเงียบครบเวลาแล้วเรียก onSilence หนึ่งครั้ง
// (เรียกใน goroutine แยกของตัวเอง)
func (a *AudioMonitor) Start(ctx context.Context, onSilence func()) error {
	const sampleRate = 16000 // ลด rate ลงเพื่อประหยัด CPU เพราะแค่เอาไว้เช็คความเงียบ ไม่ต้องละเอียดมาก
	const channels = 1

	cmd := exec.CommandContext(ctx, "parec",
		"--format=s16le",
		fmt.Sprintf("--rate=%d", sampleRate),
		fmt.Sprintf("--channels=%d", channels),
		"-d", a.source,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("เปิด stdout ของ parec ไม่สำเร็จ: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("เรียก parec ไม่สำเร็จ (ติดตั้ง pipewire-pulse หรือยัง?): %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// อ่านทีละ chunk ~100ms
	chunkSamples := sampleRate / 10
	buf := make([]byte, chunkSamples*2) // 2 bytes ต่อ sample (s16le)

	var silenceStarted time.Time
	inSilence := false

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, err := io.ReadFull(stdout, buf)
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("อ่านข้อมูลเสียงจาก parec ผิดพลาด: %w", err)
		}

		amp := rmsAmplitude(buf)

		if amp < a.amplitudeThresh {
			if !inSilence {
				inSilence = true
				silenceStarted = time.Now()
			} else if time.Since(silenceStarted) >= a.silenceDuration {
				onSilence()
				return nil // แจ้งครั้งเดียวแล้วจบ ให้ฝั่ง caller ตัดสินใจว่าจะเริ่มฟังใหม่มั้ย
			}
		} else {
			inSilence = false
		}
	}
}

// rmsAmplitude คำนวณค่า RMS ของ PCM 16-bit signed little-endian แล้ว normalize เป็น 0.0-1.0
func rmsAmplitude(buf []byte) float64 {
	n := len(buf) / 2
	if n == 0 {
		return 0
	}
	var sumSquares float64
	for i := 0; i < n; i++ {
		sample := int16(binary.LittleEndian.Uint16(buf[i*2 : i*2+2]))
		v := float64(sample) / 32768.0
		sumSquares += v * v
	}
	return math.Sqrt(sumSquares / float64(n))
}
