package main

import (
	"flag"
	"fmt"
	"os"
	"golang.org/x/exp/io/i2c"
	"time"
	"strconv"
	"net/http"
	"strings"
)

const CHAN0 = byte(0x0C)
const CHAN1 = byte(0x0E)

const TSL_ADDR = int(0x39)
const TSL_CMD = byte(0x80)

const CMD_ON = byte(0x03)
const CMD_OFF = byte(0x00)

const LOW_SHORT = byte(0x00) // x1 Gain 13.7 miliseconds
const LOW_MED = byte(0x01)   // x1 Gain 101 miliseconds
const LOW_LONG = byte(0x02)  // x1 Gain 402 miliseconds

const HIGH_SHORT = byte(0x10) // LowLight x16 Gain 13.7 miliseconds
const HIGH_MED = byte(0x11)   // LowLight x16 Gain 100 miliseconds
const HIGH_LONG = byte(0x12)  // LowLight x16 Gain 402 miliseconds

var verbose, continuous bool
var gainLevel, sleepTime int
var threshold uint64
var device, bootUrl, notifyUrl, resolvedUrl, reportHours string

func init() {
	flag.BoolVar(&verbose, "v", false, "Add verbosity to command")
	flag.BoolVar(&continuous, "c", false, "Continuous reading")
	flag.BoolVar(&continuous, "continuous", false, "Continuous reading")
	flag.IntVar(&gainLevel, "g", 1, "Gain level 1-6")
	flag.IntVar(&gainLevel, "gain", 1, "Gain level 1-6")
	flag.StringVar(&device, "d", "/dev/i2c-1", "Device, e.g. /dev/i2c-1")
	flag.StringVar(&device, "device", "/dev/i2c-1", "Device, e.g. /dev/i2c-1")
	flag.Uint64Var(&threshold, "t", 100, "Light treshold")
	flag.Uint64Var(&threshold, "threshold", 100, "Light treshold")
	flag.StringVar(&bootUrl, "b", "", "https://server/path/boot")
	flag.StringVar(&bootUrl, "bootUrl", "", "https://server/path/boot")
	flag.StringVar(&notifyUrl, "n", "", "https://server/path/notify")
	flag.StringVar(&notifyUrl, "notifyUrl", "", "https://server/path/notify")
	flag.StringVar(&resolvedUrl, "r", "", "https://server/path/resolved")
	flag.StringVar(&resolvedUrl, "resolvedUrl", "", "https://server/path/resolved")
	flag.IntVar(&sleepTime, "z", 10, "The number of minutes to sleep between each check")
	flag.IntVar(&sleepTime, "sleepTime", 10, "The number of minutes to sleep between each check")
	flag.StringVar(&reportHours, "reportHours", "09,12,16", "Specify a comma-separated list of hours when to get a reminder when light is on. Default 09,12,16")
}

func errState(message string) {
	fmt.Println(message)
	os.Exit(2)
}

func assertError(err error) {
	if err != nil {
		errState(err.Error())
	}
}

func getGain(level int) byte {

	switch level {
	case 1:
		return LOW_SHORT
	case 2:
		return LOW_MED
	case 3:
		return LOW_LONG
	case 4:
		return HIGH_SHORT
	case 5:
		return HIGH_MED
	default:
		return HIGH_LONG
	}

}

func readLight() bool {
	device, err := i2c.Open(&i2c.Devfs{Dev: device}, TSL_ADDR)
	assertError(err)

	gain := getGain(gainLevel)

	powerOn(device)
	setGain(device, gain)
	defer powerOff(device)
	defer device.Close()

	return readSensorValue(device)
}

func powerOn(device *i2c.Device) {
	device.WriteReg(TSL_CMD, []byte{CMD_ON}) //Power On
	time.Sleep(10 * time.Millisecond)
}

func powerOff(device *i2c.Device) {
	device.WriteReg(TSL_CMD, []byte{CMD_OFF}) //Power On
	time.Sleep(10 * time.Millisecond)
}

func setGain(device *i2c.Device, value byte) {
	device.WriteReg(0x01|TSL_CMD, []byte{value})
	time.Sleep(20 * time.Millisecond)
}

func readSensorValue(device *i2c.Device) bool {

	var value uint64;
	for i := 0; i < 5; i++ {

		result0 := allocWord()
		result1 := allocWord()

		device.ReadReg(CHAN0|TSL_CMD, result0)
		time.Sleep(10 * time.Millisecond)
		device.ReadReg(CHAN1|TSL_CMD, result1)

		ch0 := uint64(result0[1])*256 + uint64(result0[0])
		ch1 := uint64(result1[1])*256 + uint64(result1[0])

		vResult := ch0 - ch1

		value += vResult
	}

	value = value / 5

	valueAsString := strconv.FormatUint

	sout("Sensor value: ", valueAsString(value, 10))

	if value < threshold {
		return false
	} else {
		return true
	}
}

func allocWord() []byte {
	return make([]byte, 2)
}

func readOnce() {
	isLit := readLight()

	if isLit {
		if verbose {
			fmt.Println("Light is on")
		} else {
			fmt.Println("1")
		}
	} else {
		if verbose {
			fmt.Println("Light is off")
		} else {
			fmt.Println("0")
		}
	}
}

func has(value string) bool {
	return value != ""
}

func sendMessage(url string) {

	// TODO, put message on a queue and have a go-routine retry until message can be sent
	// If queue grows to big, reboot the machine

	var guard int = 10
	var resp *http.Response

	var err error = nil
	for {
		guard--
		if guard <= 0 {
			sout("Could not send message in time, giving up")
		}

		resp, err = http.Get(url)

		if err == nil {
			sout("Sent request to", url, "which returned:", resp.StatusCode)
			return
		}

		sout("Could not send message, waiting a bit")
		time.Sleep(5 * time.Second)
	}

}

func readContinuous() {

	var previousLit = false

	var doSendMessage = true

	if has(bootUrl) {
		sendMessage(bootUrl)
	}

	for {
		now := time.Now();

		// Check at noon if message should be sent again
		if isReminderTime(now.Hour(), reportHours) && doSendMessage == true {
			doSendMessage = false
			previousLit = false
		} else if !isReminderTime(now.Hour(), reportHours) {
			doSendMessage = true
		}

		isLit := readLight()

		if isLit && (previousLit == false) && isReportTime(now.Hour()) {
			previousLit = true
			sendMessage(notifyUrl)
		} else if !isLit {
			if previousLit == true {
				sendMessage(resolvedUrl)
			}

			previousLit = false
		}

		time.Sleep(time.Duration(sleepTime) * time.Minute)
	}
}


func isReportTime(hour int) bool {
	return hour > 8 && hour < 22
}

func isReminderTime(hour int, reportHours string) bool {
	hourAsString := strconv.Itoa(hour)

	return strings.Index(reportHours, hourAsString) > -1
}

func sout(a ...interface{}) {
	if verbose {
		fmt.Println(a)
	}
}

func main() {
	flag.Parse()

	sout("** Welcome to Pi Light Sensor **")

	if continuous {

		if notifyUrl == "" {
			errState("The --notifyUrl parameter must be set in continuous mode")
		}

		readContinuous()
	} else {
		readOnce()
	}
}
