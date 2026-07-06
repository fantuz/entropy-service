package diag

// ClientRateMeter is the global meter used by entropy client code.
var ClientRateMeter = NewRateMeter()

// Previous initialisation, kept for reference:
// var ClientRateMeter *RateMeter
//
// //var ClientRateMeter = NewRateMeter()
//
// func init() {
// 	ClientRateMeter = NewRateMeter()
// }
