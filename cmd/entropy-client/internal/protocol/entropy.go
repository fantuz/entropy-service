package protocol

type EntropyResponse struct {
	Bytes []byte `json:"bytes"`
	Size  int    `json:"size"`
}
