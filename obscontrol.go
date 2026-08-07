// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"fmt"

	"github.com/andreykaipov/goobs"
)

// OBSController ห่อการเชื่อมต่อ OBS WebSocket ไว้ใช้งานง่ายๆ แค่ start/stop record
type OBSController struct {
	client *goobs.Client
}

func NewOBSController(host, password string) (*OBSController, error) {
	client, err := goobs.New(host, goobs.WithPassword(password))
	if err != nil {
		return nil, fmt.Errorf("เชื่อมต่อ OBS ไม่สำเร็จ: %w", err)
	}
	return &OBSController{client: client}, nil
}

func (o *OBSController) StartRecord() error {
	_, err := o.client.Record.StartRecord()
	if err != nil {
		return fmt.Errorf("สั่งเริ่มอัดไม่สำเร็จ: %w", err)
	}
	return nil
}

func (o *OBSController) StopRecord() error {
	_, err := o.client.Record.StopRecord()
	if err != nil {
		return fmt.Errorf("สั่งหยุดอัดไม่สำเร็จ: %w", err)
	}
	return nil
}

func (o *OBSController) Close() {
	if o.client != nil {
		o.client.Disconnect()
	}
}
