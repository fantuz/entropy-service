package lab

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // ONVIF UsernameToken PasswordDigest mandates SHA-1.
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type soapEnvelope struct {
	Body struct {
		Capabilities struct {
			Capabilities struct {
				Media struct {
					Address string `xml:"XAddr"`
				} `xml:"Media"`
			} `xml:"Capabilities"`
		} `xml:"GetCapabilitiesResponse"`
		Profiles struct {
			Profiles []struct {
				Token string `xml:"token,attr"`
			} `xml:"Profiles"`
		} `xml:"GetProfilesResponse"`
		Stream struct {
			URI struct {
				Value string `xml:"Uri"`
			} `xml:"MediaUri"`
		} `xml:"GetStreamUriResponse"`
		Fault *struct{} `xml:"Fault"`
	} `xml:"Body"`
}

func xmlText(value string) string {
	var out bytes.Buffer

	_ = xml.EscapeText(&out, []byte(value))

	return out.String()
}

func securityHeader(credentials Credentials) (string, error) {
	if credentials.Username == "" {
		return "", nil
	}

	nonce := make([]byte, 20)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	created := time.Now().UTC().Format(time.RFC3339)
	digest := sha1.New() //nolint:gosec // Required ONVIF UsernameToken interoperability, not RNG hashing.
	_, _ = digest.Write(nonce)
	_, _ = digest.Write([]byte(created))
	_, _ = digest.Write([]byte(credentials.Password))

	return `<s:Header><wsse:Security s:mustUnderstand="1" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken><wsse:Username>` + xmlText(credentials.Username) + `</wsse:Username><wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + base64.StdEncoding.EncodeToString(digest.Sum(nil)) + `</wsse:Password><wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` + base64.StdEncoding.EncodeToString(nonce) + `</wsse:Nonce><wsu:Created>` + created + `</wsu:Created></wsse:UsernameToken></wsse:Security></s:Header>`, nil
}

func (r *Registry) soap(ctx context.Context, endpoint, action, body string, credentials Credentials) (soapEnvelope, error) {
	var result soapEnvelope

	header, err := securityHeader(credentials)
	if err != nil {
		return result, err
	}

	payload := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">` + header + `<s:Body>` + body + `</s:Body></s:Envelope>`

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return result, errors.New("invalid ONVIF service")
	}

	request.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="`+action+`"`)

	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.New("ONVIF service unreachable")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return result, errors.New("ONVIF service rejected request; check credentials and device clock")
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return result, errors.New("invalid ONVIF response size")
	}

	if err := xml.Unmarshal(data, &result); err != nil || result.Body.Fault != nil {
		return result, errors.New("ONVIF SOAP request failed")
	}

	return result, nil
}

func sameCameraHost(raw string, base *url.URL, rtsp bool) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || !strings.EqualFold(u.Hostname(), base.Hostname()) || u.User != nil || u.Fragment != "" {
		return false
	}

	if rtsp {
		return u.Scheme == sourceRTSP
	}

	return sameOrigin(u, base)
}

func (r *Registry) onvifURI(ctx context.Context, config Config, credentials Credentials) (string, error) {
	base, _ := url.Parse(config.Endpoint)

	capabilities, err := r.soap(ctx, config.Endpoint, "http://www.onvif.org/ver10/device/wsdl/GetCapabilities", `<tds:GetCapabilities><tds:Category>Media</tds:Category></tds:GetCapabilities>`, credentials)
	if err != nil {
		return "", err
	}

	media := capabilities.Body.Capabilities.Capabilities.Media.Address
	if !sameCameraHost(media, base, false) {
		return "", errors.New("ONVIF Media service must stay on the configured device origin")
	}

	token := config.ProfileToken
	if token == "" {
		profiles, err := r.soap(ctx, media, "http://www.onvif.org/ver10/media/wsdl/GetProfiles", `<trt:GetProfiles/>`, credentials)
		if err != nil {
			return "", err
		}

		if len(profiles.Body.Profiles.Profiles) == 0 {
			return "", errors.New("ONVIF device has no media profiles")
		}

		token = profiles.Body.Profiles.Profiles[0].Token
	}

	if token == "" || len(token) > 256 {
		return "", errors.New("invalid ONVIF media profile")
	}

	stream, err := r.soap(ctx, media, "http://www.onvif.org/ver10/media/wsdl/GetStreamUri", `<trt:GetStreamUri><trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup><trt:ProfileToken>`+xmlText(token)+`</trt:ProfileToken></trt:GetStreamUri>`, credentials)
	if err != nil {
		return "", err
	}

	uri := stream.Body.Stream.URI.Value
	if !sameCameraHost(uri, base, true) {
		return "", errors.New("ONVIF stream URI must stay on the configured camera host")
	}

	return uri, nil
}
