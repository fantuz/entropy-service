package lab

import "runtime"

// SupportedKinds describes registrations this server can accept. Cameras also
// need FFmpeg and an accessible camera; browser-local sources need no registry.
func SupportedKinds() []string {
	return supportedKinds(runtime.GOOS)
}

func supportedKinds(goos string) []string {
	kinds := []string{
		sourceHTTP, sourceHTTPS, sourceWS, sourceEntropyHTTP, sourceEntropyWS,
		sourceTCP, sourceUDP, sourceWebpage, sourceRTSP, sourceONVIF,
	}
	if goos != windowsOS {
		kinds = append(kinds, sourceUnix)
	}

	if goos == linuxOS {
		kinds = append(kinds, sourceDevice)
	}

	return kinds
}
