package fastboot

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const Binary = "fastboot"

const PlatformTools = "https://developer.android.com/tools/releases/platform-tools"

var ErrNoDevice = errors.New("no device in fastboot mode")

func Require() error {
	if _, err := exec.LookPath(Binary); err != nil {
		return fmt.Errorf("fastboot is not on PATH: install Android platform-tools and try again\n"+
			"  %s\n"+
			"  macOS: brew install --cask android-platform-tools\n"+
			"  Debian/Ubuntu: apt install android-sdk-platform-tools", PlatformTools)
	}
	return nil
}

type Device struct{ serial string }

func (d *Device) Serial() string { return d.serial }

func List() ([]string, error) {
	out, err := run(context.Background(), "devices")
	if err != nil {
		return nil, err
	}
	var serials []string
	for line := range strings.SplitSeq(out, "\n") {
		serial, state, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && strings.TrimSpace(state) == "fastboot" {
			serials = append(serials, serial)
		}
	}
	return serials, nil
}

func Attach(serial string) (*Device, error) {
	if err := Require(); err != nil {
		return nil, err
	}
	serials, err := List()
	if err != nil {
		return nil, err
	}
	switch {
	case len(serials) == 0:
		return nil, ErrNoDevice
	case serial != "":
		for _, s := range serials {
			if s == serial {
				return &Device{serial: s}, nil
			}
		}
		return nil, fmt.Errorf("fastboot: no device with serial %q", serial)
	case len(serials) > 1:
		return nil, fmt.Errorf("fastboot: %d devices connected, name one: %s",
			len(serials), strings.Join(serials, ", "))
	}
	return &Device{serial: serials[0]}, nil
}

func WaitForDevice(ctx context.Context, serial string) (*Device, error) {
	if err := Require(); err != nil {
		return nil, err
	}
	for {
		d, err := Attach(serial)
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, ErrNoDevice) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (d *Device) Getvar(name string) (string, error) {
	out, err := d.run(context.Background(), "getvar", name)
	if err != nil {
		return "", err
	}
	prefix := name + ":"
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("fastboot: getvar %s had no answer in %q", name, out)
}

func (d *Device) Flash(partition, file string) error {
	_, err := d.run(context.Background(), "flash", partition, file)
	return err
}

func (d *Device) OEM(args ...string) error {
	_, err := d.run(context.Background(), append([]string{"oem"}, args...)...)
	return err
}

func (d *Device) run(ctx context.Context, args ...string) (string, error) {
	return run(ctx, append([]string{"-s", d.serial}, args...)...)
}

func run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, Binary, args...)
	// fastboot writes getvar replies and progress to stderr.
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		said := strings.Join(strings.Fields(text), " ")
		return text, fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, said)
	}
	return text, nil
}
