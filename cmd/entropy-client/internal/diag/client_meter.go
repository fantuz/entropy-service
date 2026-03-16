package diag

// Global meter used by entropy client code
var ClientRateMeter *RateMeter
//var ClientRateMeter = NewRateMeter()

func init() {
    ClientRateMeter = NewRateMeter()
}
