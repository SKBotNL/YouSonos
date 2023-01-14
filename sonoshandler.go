// Copyright 2022 SKBotNL
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type FormatStream struct {
	Url        string `json:"url"`
	Container  string `json:"container"`
	Resolution string `json:"resolution"`
}

type Invidious struct {
	Title         string         `json:"title"`
	FormatStreams []FormatStream `json:"formatStreams"`
	LengthSeconds int            `json:"lengthSeconds"`
}

type Envelope struct {
	Body struct {
		GetVolumeResponse struct {
			CurrentVolume int `xml:"CurrentVolume"`
		} `xml:"GetVolumeResponse"`
	} `xml:"Body"`
}

var invidiousBaseUrl = "https://invidious.namazso.eu"

func sonosHandler(ytUrl string) (int, string, string, error) {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/AVTransport/Control"

	xml := `<?xml version="1.0"?>
		<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
			<s:Body>
				<u:SetAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
					<InstanceID>0</InstanceID>
					<CurrentURI>%s</CurrentURI>
					<CurrentURIMetaData>%s</CurrentURIMetaData>
				</u:SetAVTransportURI>
			</s:Body>
		</s:Envelope>`

	title, audioStream, thumbnail, lengthSeconds, ytId, err := getYtData(ytUrl)
	if err != nil {
		return 0, "", "", err
	}

	id := len(redirMap) + 1
	redirMap[id] = audioStream

	localIp := ""

	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if strings.HasSuffix(addr.String(), "/24") {
			localIp = strings.Replace(addr.String(), "/24", "", -1)
			break
		}
	}

	uri := fmt.Sprintf("http://%s:9372/%d.mp4", localIp, id)

	enqueuedURIMetaData := createMetaData(uri, title, thumbnail)

	body := fmt.Sprintf(xml, html.EscapeString(uri), html.EscapeString(enqueuedURIMetaData))

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(body)))
	if err != nil {
		return 0, "", "", err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()

	return lengthSeconds, ytId, title, nil
}

func createMetaData(audioUri string, title string, artUri string) string {
	xml := `<DIDL-Lite
				xmlns:dc="http://purl.org/dc/elements/1.1/"
				xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"
				xmlns:r="urn:schemas-rinconnetworks-com:metadata-1-0/"
				xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/">
				<item id="-1" parentID="-1" restricted="true">
					<res protocolInfo="http-get:*:audio/mp4:*">%s</res>
					<r:streamContent></r:streamContent>
					<dc:title>%s</dc:title>
					<upnp:class>object.item.audioItem.musicTrack</upnp:class>
					<dc:creator></dc:creator>
					<upnp:album></upnp:album>
					<upnp:albumArtURI>%s</upnp:albumArtURI>
				</item>
			</DIDL-Lite>`

	body := fmt.Sprintf(xml, audioUri, title, artUri)
	return body
}

func getYtData(ytUrl string) (string, string, string, int, string, error) {
	r := regexp.MustCompile(`^(?:https?:)?(?:\/\/)?(?:youtu\.be\/|(?:www\.|m\.)?youtube\.com\/(?:watch|v|embed)(?:\.php)?(?:\?.*v=|\/))([a-zA-Z0-9\_-]{7,15})(?:[\?&][a-zA-Z0-9\_-]+=[a-zA-Z0-9\_-]+)*$`)
	match := r.Match([]byte(ytUrl))
	if !match {
		return "", "", "", 0, "", errors.New("url is not a YouTube url")
	}

	id := r.FindStringSubmatch(ytUrl)[1]

	ivUrl := fmt.Sprintf("%s/api/v1/videos/%s", invidiousBaseUrl, id)

	req, err := http.NewRequest("GET", ivUrl, nil)
	if err != nil {
		return "", "", "", 0, "", err
	}

	req.Header.Add("User-Agent", "YouSonos")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", 0, "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", 0, "", err
	}

	res := Invidious{}
	err = json.Unmarshal([]byte(bodyBytes), &res)
	if err != nil {
		return "", "", "", 0, "", err
	}

	stream := ""

	for i := range res.FormatStreams {
		formatStream := res.FormatStreams[i]
		if formatStream.Resolution == "360p" && formatStream.Container == "mp4" {
			stream = formatStream.Url
			break
		}
	}

	uLink, _ := url.Parse(stream)
	replace := strings.Replace(stream, fmt.Sprintf("https://%s/", uLink.Host), "", 1)
	stream = fmt.Sprintf("%s/%s", invidiousBaseUrl, replace)

	thumbnail := fmt.Sprintf("https://i.ytimg.com/vi/%s/maxresdefault.jpg", id)

	return res.Title, stream, thumbnail, res.LengthSeconds, id, nil
}

func play() error {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/AVTransport/Control"

	xml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
						<InstanceID>0</InstanceID>
						<Speed>1</Speed>
					</u:Play>
				</s:Body>
			</s:Envelope>`

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(xml)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:AVTransport:1#Play"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func pause() error {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/AVTransport/Control"

	xml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:Pause xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
						<InstanceID>0</InstanceID>
					</u:Pause>
				</s:Body>
			</s:Envelope>`

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(xml)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:AVTransport:1#Pause"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func stop() error {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/AVTransport/Control"

	xml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:Stop xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
						<InstanceID>0</InstanceID>
					</u:Stop>
				</s:Body>
			</s:Envelope>`

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(xml)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:AVTransport:1#Stop"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func seek(seconds int) error {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/AVTransport/Control"

	xml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:Seek xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
						<InstanceID>0</InstanceID>
						<Unit>REL_TIME</Unit>
						<Target>%s</Target>
					</u:Seek>
				</s:Body>
			</s:Envelope>`

	hour := int(seconds / 3600)
	minute := int(seconds/60) % 60
	second := seconds % 60
	hms := fmt.Sprintf("%02d:%02d:%02d\n", hour, minute, second)

	body := fmt.Sprintf(xml, hms)

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:AVTransport:1#Seek"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func getVolume() (int, error) {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/RenderingControl/Control"

	rawXml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:GetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">
						<InstanceID>0</InstanceID>
						<Channel>Master</Channel>
					</u:GetVolume>
				</s:Body>
			</s:Envelope>`

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(rawXml)))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:RenderingControl:1#GetVolume"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	envelope := Envelope{}
	xml.Unmarshal([]byte(bodyBytes), &envelope)

	return envelope.Body.GetVolumeResponse.CurrentVolume, nil
}

func setVolume(volume int) error {
	u, _ := url.Parse(selectedDevice.Host)
	u.Path = "/MediaRenderer/RenderingControl/Control"

	xml := `<?xml version="1.0"?>
			<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
				<s:Body>
					<u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">
						<InstanceID>0</InstanceID>
						<Channel>Master</Channel>
						<DesiredVolume>%d</DesiredVolume>
					</u:SetVolume>
				</s:Body>
			</s:Envelope>`

	body := fmt.Sprintf(xml, volume)

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/xml; charset=\"utf8\"")
	req.Header["SOAPACTION"] = []string{"urn:schemas-upnp-org:service:RenderingControl:1#SetVolume"}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
