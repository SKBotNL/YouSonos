package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type Root struct {
	Device struct {
		RoomName    string `xml:"roomName"`
		DisplayName string `xml:"displayName"`
	} `xml:"device"`
}

type Device struct {
	Name string
	Host string
}

var tick = false
var globalSeconds = 0
var songSeconds = 0
var lastSeek time.Time
var seekActive = false
var sliderValue int
var selectedDevice Device
var sonosDevices = make(map[string]string)
var channel = make(chan bool)
var playing = false

func main() {
	go redirector()

	a := app.NewWithID("nl.skbotnl.yousonos")
	w := a.NewWindow("YouSonos")

	activeDevice := a.Preferences().String("ActiveDevice")
	if activeDevice == "" {
		dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
	}

	devices, err := searchDevices()
	if err != nil {
		dialog.ShowError(err, w)
	}

	for _, dev := range devices {
		loc := dev.Get("Location")

		resp, err := http.Get(loc)
		if err != nil {
			dialog.ShowError(err, w)
			break
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			dialog.ShowError(err, w)
			break
		}

		root := Root{}

		err = xml.Unmarshal(bodyBytes, &root)
		if err != nil {
			dialog.ShowError(err, w)
			break
		}

		u, err := url.Parse(loc)
		if err != nil {
			dialog.ShowError(err, w)
			break
		}

		host := u.Host

		sonosDevices[fmt.Sprintf("%s (%s)", root.Device.RoomName, root.Device.DisplayName)] = host
	}

	if activeDevice != "" {
		host, ok := sonosDevices[activeDevice]
		if !ok {
			dialog.NewError(errors.New("could not find selected device"), w)
		}

		selectedDevice = Device{
			Name: activeDevice,
			Host: "http://" + host,
		}
	}

	makeTray(a, w)

	input := widget.NewEntry()
	input.SetPlaceHolder("Enter Youtube URL...")

	positionLabel := widget.NewLabel("00:00:00")
	slider := widget.NewSlider(0, 0)

	slider.OnChanged = func(value float64) {
		lastSeek = time.Now()
		sliderValue = int(value)

		hour := int(sliderValue / 3600)
		minute := int(sliderValue/60) % 60
		second := sliderValue % 60
		hms := fmt.Sprintf("%02d:%02d:%02d\n", hour, minute, second)
		positionLabel.Text = hms
		positionLabel.Refresh()

		if !seekActive {
			seekActive = true
			go func() {
				for {
					time.Sleep(100 * time.Millisecond)
					diff := time.Since(lastSeek)
					if diff.Milliseconds() >= 500 {
						break
					}
				}
				seek(sliderValue)

				globalSeconds = sliderValue

				seekActive = false
			}()
		}
	}

	currentVolume := 0
	if (Device{}) != selectedDevice {
		currentVolume, err = getVolume()
		if err != nil {
			dialog.ShowError(err, w)
		}
	}

	volumeLabel := widget.NewLabel(fmt.Sprintf("%d%%", currentVolume))
	volumeSlider := widget.NewSlider(0, 100)
	volumeSlider.SetValue(float64(currentVolume))

	volumeSlider.OnChanged = func(value float64) {
		if (Device{}) == selectedDevice {
			dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
			return
		}

		volume := int(value)
		err := setVolume(volume)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		volumeLabel.Text = fmt.Sprintf("%d%%", volume)
		volumeLabel.Refresh()
	}

	goButton := widget.NewButton("Go", nil)

	playButton := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), nil)
	playButton.OnTapped = func() {
		if (Device{}) == selectedDevice {
			dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
			return
		}

		if playing {
			tick = false
			playing = false
			err := pause()
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			playButton.Icon = theme.MediaPlayIcon()
			playButton.Refresh()
		} else {
			if slider.Value >= float64(songSeconds) {
				slider.Value = 0
				globalSeconds = 0
			}
			tick = true
			playing = true
			err := play()
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			playButton.Icon = theme.MediaPauseIcon()
			playButton.Refresh()
		}
	}
	// pauseButton := widget.NewButton("Pause", func() {
	// 	if (Device{}) == selectedDevice {
	// 		dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
	// 		return
	// 	}
	// })

	stopButton := widget.NewButtonWithIcon("", theme.MediaStopIcon(), nil)

	settingsButton := widget.NewButton("Settings", func() {
		openSettings(a, *slider, *positionLabel)
	})

	playingLabel := widget.NewLabel("Nothing is playing")

	image := canvas.NewImageFromResource(resourceEmptythumbnailPng)
	image.SetMinSize(fyne.NewSize(200, 200))
	image.FillMode = canvas.ImageFillContain

	// sliderHBox := container.NewHBox(slider, positionLabel)
	playingCenter := container.NewCenter(playingLabel)
	videoBorder := container.NewBorder(nil, playingCenter, nil, nil, image)
	buttonsBox := container.NewHBox(playButton, stopButton)
	buttonsCenter := container.NewCenter(buttonsBox)
	// buttonsBorder := container.NewBorder(nil, nil, playButton, stopButton)
	imageBorder := container.NewBorder(videoBorder, buttonsCenter, nil, nil)

	inputBorder := container.NewBorder(nil, nil, nil, goButton, input)
	sliderBorder := container.NewBorder(nil, nil, nil, positionLabel, slider)
	volumeBorder := container.NewBorder(nil, nil, widget.NewIcon(theme.MediaMusicIcon()), volumeLabel, volumeSlider)

	content := container.NewVBox(imageBorder, inputBorder, settingsButton, sliderBorder, volumeBorder)

	goButton.OnTapped = func() {
		if (Device{}) == selectedDevice {
			dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
			return
		}

		id := ""
		title := ""
		songSeconds, id, title, err = sonosHandler(input.Text)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		go func() {
			readcloser, err := loadData(id)
			if err != nil {
				dialog.NewError(err, w)
			}

			defer readcloser.Close()

			image = canvas.NewImageFromReader(readcloser.(io.Reader), "maxresdefault.jpg")
			image.SetMinSize(fyne.NewSize(200, 200))
			image.FillMode = canvas.ImageFillContain

			videoBorder := container.NewBorder(nil, playingCenter, nil, nil, image)
			imageBorder := container.NewBorder(videoBorder, buttonsCenter, nil, nil)
			content = container.NewVBox(imageBorder, inputBorder, settingsButton, sliderBorder, volumeBorder)

			w.SetContent(content)
		}()

		err = play()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		playingLabel.Text = title
		playingLabel.Refresh()
		playButton.Icon = theme.MediaPauseIcon()
		playButton.Refresh()

		slider.Max = float64(songSeconds)
		slider.Value = 0
		globalSeconds = 0
		tick = true
		playing = true
	}

	stopButton.OnTapped = func() {
		if (Device{}) == selectedDevice {
			dialog.ShowInformation("No device selected", "Go to the settings to select a device", w)
			return
		}

		slider.Max = 0
		slider.Value = 0
		globalSeconds = 0
		slider.Refresh()

		positionLabel.Text = "00:00:00"
		positionLabel.Refresh()

		playing = false
		playButton.Icon = theme.MediaPlayIcon()
		playButton.Refresh()

		playingLabel.Text = "Nothing is playing"
		playingLabel.Refresh()

		go func() {
			image = canvas.NewImageFromResource(resourceEmptythumbnailPng)
			image.SetMinSize(fyne.NewSize(200, 200))
			image.FillMode = canvas.ImageFillContain

			videoBorder := container.NewBorder(nil, playingCenter, nil, nil, image)
			imageBorder := container.NewBorder(videoBorder, buttonsCenter, nil, nil)
			content = container.NewVBox(imageBorder, inputBorder, settingsButton, sliderBorder, volumeBorder)

			w.SetContent(content)
		}()

		tick = false
		err := stop()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
	}

	w.SetContent(content)

	go func() {
		for range time.Tick(1 * time.Second) {
			if tick {
				if int(slider.Value) >= songSeconds {
					tick = false
					continue
				}
				labelInt, _ := strconv.Atoi(positionLabel.Text)
				positionLabel.Text = fmt.Sprint(labelInt + 1)

				globalSeconds += 1
				hour := int(globalSeconds / 3600)
				minute := int(globalSeconds/60) % 60
				second := globalSeconds % 60
				hms := fmt.Sprintf("%02d:%02d:%02d\n", hour, minute, second)
				positionLabel.Text = hms
				positionLabel.Refresh()

				slider.Value += 1
				slider.Refresh()
			}
		}
	}()

	// Channel for setting everything to 0, because when called from openSettings it doesn't actually refresh
	go func() {
		for {
			<-channel
			slider.Max = 0
			slider.Value = 0
			globalSeconds = 0
			slider.Refresh()
			tick = false
			positionLabel.Text = "00:00:00"
			positionLabel.Refresh()

			currentVolume, err := getVolume()
			if err != nil {
				dialog.ShowError(err, w)
				continue
			}
			volumeSlider.SetValue(float64(currentVolume))
		}
	}()

	w.SetCloseIntercept(func() {
		w.Hide()
	})

	w.Resize(fyne.NewSize(600, 400))
	w.ShowAndRun()

	// var wg sync.WaitGroup
	// wg.Add(1)
	// wg.Wait()
}

func openSettings(a fyne.App, slider widget.Slider, positionLabel widget.Label) {
	w := a.NewWindow("Settings")

	names := make([]string, len(sonosDevices))

	i := 0
	for a := range sonosDevices {
		names[i] = a
		i++
	}

	selectWidget := widget.NewSelect(names, func(selected string) {
		if selected == selectedDevice.Name {
			return
		}
		selectedDevice = Device{
			Name: selected,
			Host: "http://" + sonosDevices[selected],
		}
		a.Preferences().SetString("ActiveDevice", selected)

		channel <- true
	})

	if (Device{}) != selectedDevice {
		selectWidget.Selected = selectedDevice.Name
	}

	vbox := container.NewVBox(selectWidget)
	w.SetContent(vbox)

	w.Resize(fyne.NewSize(600, 400))
	w.Show()
}

func makeTray(a fyne.App, w fyne.Window) {
	if desk, ok := a.(desktop.App); ok {
		show := fyne.NewMenuItem("Show", func() {
			w.Show()
		})

		menu := fyne.NewMenu("YouSonos", show)
		menu.Label = "YouSonos"
		desk.SetSystemTrayMenu(menu)
	}
}

func searchDevices() ([]http.Header, error) {
	query := "urn:schemas-upnp-org:device:ZonePlayer:1"

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := strings.Join([]string{
		"M-SEARCH * HTTP/1.1",
		"HOST: 239.255.255.250:1900",
		"MAN: \"ssdp:discover\"",
		"ST: " + query,
		"MX: 1",
	}, "\r\n")

	addr, err := net.ResolveUDPAddr("udp", "239.255.255.250:1900")
	if err != nil {
		return nil, err
	}

	_, err = conn.WriteTo([]byte(req), addr)
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	var devices []http.Header
	for {
		buf := make([]byte, 65536)

		n, _, err := conn.ReadFrom(buf)
		if err, ok := err.(net.Error); ok && err.Timeout() {
			break
		} else if err != nil {
			log.Printf("ReadFrom error: %s", err)
			break
		}

		r := bufio.NewReader(bytes.NewReader(buf[:n]))

		resp, err := http.ReadResponse(r, &http.Request{})
		if err != nil {
			return nil, err
		}
		resp.Body.Close()

		for _, head := range resp.Header["St"] {
			if head == query {
				devices = append(devices, resp.Header)
				break
			}
		}
	}

	return devices, nil
}

func loadData(id string) (io.ReadCloser, error) {
	ytimg := fmt.Sprintf("https://i.ytimg.com/vi/%s/maxresdefault.jpg", id)
	req, err := http.NewRequest("GET", ytimg, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}
