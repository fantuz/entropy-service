package tests


import (
	"fmt"
	"entropy-service/internal/diag"
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

	//result.Rate = r.Update()
	fmt.Println("Entropy diagnostics")
	fmt.Println("-------------------")

	fmt.Printf("bytes              : %d\n", result.N)
	fmt.Printf("shannon entropy    : %.5f / 8\n", result.Shannon)
	fmt.Printf("chi-square         : %.3f (p=%.5f)\n", result.Chi2, result.Chi2P)
	fmt.Printf("monobit p-value    : %.5f\n", result.MonobitP)
	fmt.Printf("serial correlation : %.6f (p=%.5f)\n", result.SerialR, result.SerialP)
	fmt.Printf("entropy rate       : %.2f Mbit/s\n", result.Rate)

	if result.Pass {
		fmt.Println("RESULT             : " + Green + "OK" + Reset)
	} else {
		fmt.Println("RESULT             : " + Red + "WARNING" + Reset)
	}

	fmt.Println()

}
