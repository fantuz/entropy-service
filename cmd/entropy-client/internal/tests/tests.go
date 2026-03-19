package tests


import (
	"fmt"
	"github.com/fantuz/entropy-service/entropy-client/internal/diag"
)

const Reset = "\033[0m"
const Red = "\033[31m"
const Green = "\033[32m"

/*
func runTests(data []byte) {

	tests := []Test{
		ChiSquareTest{},
		MonobitTest{},
		SerialTest{},
	}

	for _, t := range tests {

		r := t.Run(data)

		status := "FAIL"
		if r.Pass {
			status = "PASS"
		}

		fmt.Printf("%-15s %s\n", t.Name(), status)
	}
}
*/

/*
func RunAll(data []byte) {
	RunChiSquare(data)
	RunHistogram(data)
	RunNIST(data)
}
*/

func RunAll(data []byte) {

	result := diag.RunDiagnostics(data)

	//t := diag.NewRateMeter()
	//t.Rate = t.Update(result)
	//t.Update(int(stats.Rate))

	fmt.Println("Entropy diagnostics")
	fmt.Println("---------------------------")

	fmt.Printf("payload size       : %d\n", result.N)
	//fmt.Printf("histogram rate     : %v\n", result.Rate)
	fmt.Printf("shannon entropy    : %.5f / 8\n", result.Shannon)
	fmt.Printf("chi-square         : %.3f (p=%.5f)\n", result.Chi2, result.Chi2P)
	fmt.Printf("monobit p-value    : %.5f\n", result.MonobitP)
	fmt.Printf("serial correlation : %.6f (p=%.5f)\n", result.SerialR, result.SerialP)
	//fmt.Printf("entropy rate r     : %.8f Mbit/s\n", result.Rate)
	//fmt.Printf("entropy rate t.rat : %.2d Mbit/s\n", &t.Rate)
	//check := strconv.Itoa(int(t.Rate))
	//fmt.Printf("entropy rate c     : %.8f Mbit/s\n", check)
	//fmt.Printf("random rate  t.met : %.2d Mbit/s\n", &t.Meter)
	//fmt.Printf("array r            : %v\n", result)
	//fmt.Printf("array t            : %v\n", t)

	if result.Pass {
		fmt.Println("result             : " + Green + "OK" + Reset)
	} else {
		fmt.Println("result             : " + Red + "WARN" + Reset)
	}

}
