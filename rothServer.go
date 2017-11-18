package roth

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
)

const (
	//ProgramConstant is no program, i.e. the same temperature setting throughout the day and week
	ProgramConstant = 0
	//Program1 is one of the three programmable programs on the thermostat
	Program1 = 1
	//Program2 is one of the three programmable programs on the thermostat
	Program2 = 2
	//Program3 is one of the three programmable programs on the thermostat
	Program3 = 3

	//ModeDay is the normal operating mode
	ModeDay = 0
	//ModeNight is night operating mode
	ModeNight = 1
	//ModeHoliday is holiday mode (no frost)
	ModeHoliday = 2
)

//Sensor represents a state of one of the Roth thermostat sensors.
type Sensor struct {
	Id                int
	Name              string
	RoomTemperature   float32
	TargetTemperature float32
	Program           int
	Mode              int
}

const (
	//ValveOpen represents a valve in its open state
	ValveOpen = "open"

	//ValveClosed represents a valve in its closed state
	ValveClosed = "closed"
)

//GetValveState returns the current state of the valve connected (open/closed) to the sensor.
//This is currently derived from room and target temperature, as the roth server does not expose
//the valve state directly.
func (s Sensor) GetValveState() string {
	if s.RoomTemperature < s.TargetTemperature {
		return ValveOpen
	}
	return ValveClosed
}

//GetValveValue returns the current state (0 is off, 1 is on) of the valveconnected to the sensor
//This is currently derived from room and target temperature, as the roth server does not expose
//the valve state directly.
func (s Sensor) GetValveValue() int32 {
	if s.RoomTemperature < s.TargetTemperature {
		return 1
	}
	return 0
}

/*
Example request: POST http://ROTH-10A6D5/cgi-bin/ILRReadValues.cgi
<body>
	<item_list>
		<i><n>G0.RaumTemp</n></i>
		<i><n>G1.RaumTemp</n></i>
	</item_list>
</body>

Example response
<body>
	<item_list>
		<i>
			<n>G0.RaumTemp</n>
			<v>2086</v>
		</i>
		<i>
			<n>G1.RaumTemp</n>
			<v>1903</v>
		</i>
	</item_list>
</body>
*/

type readRequest struct {
	Items []readRequestItem `xml:"item_list>i"`
}

type readRequestItem struct {
	Name string `xml:"n"`
}

type response struct {
	Items []responseItem `xml:"item_list>i"`
}

type responseItem struct {
	Name  string `xml:"n"`
	Value string `xml:"v"`
}

func marshalRequest(req readRequest) ([]byte, error) {
	tmp := struct {
		readRequest
		XMLName struct{} `xml:"body"`
	}{readRequest: req}

	return xml.MarshalIndent(tmp, "", "   ")
}

func readValues(managementURL string, req readRequest) (resp response, err error) {
	//Serialize request
	requstData, err := marshalRequest(req)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	//Send request
	url := fmt.Sprintf("%v/cgi-bin/ILRReadValues.cgi", managementURL)
	httpResponse, err := http.Post(url, "text/xml", bytes.NewReader(requstData))
	if err != nil {
		return response{}, errors.New("error requesting data from server")
	}
	defer httpResponse.Body.Close()
	body, err := ioutil.ReadAll(httpResponse.Body)
	if err != nil {
		return response{}, errors.New("error reading response")
	}

	//read into struct
	err = xml.Unmarshal(body, &resp)
	if err != nil {
		return response{}, errors.New("error parsing xml")
	}

	return resp, nil
}

func writeValue(managementURL string, sensorID int, valueName string, value string) error {
	//Send request
	url := fmt.Sprintf("%v/cgi-bin/writeVal.cgi?G%v.%v=%v", managementURL, sensorID, valueName, value)
	_, err := http.Get(url)
	if err != nil {
		return errors.New("error sending data to server")
	}

	return nil
}

//GetSensorCount returns the total number of sensors on the server
func GetSensorCount(managementURL string) (sensorCount int, err error) {
	req := readRequest{Items: []readRequestItem{readRequestItem{Name: "totalNumberOfDevices"}}}

	resp, err := readValues(managementURL, req)
	if err != nil {
		return 0, err
	}

	if len(resp.Items) == 0 {
		return 0, errors.New("no values returned")
	}

	intValue, err := strconv.ParseInt(resp.Items[0].Value, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("Unexpected value %v", resp.Items[0].Value)
	}

	return int(intValue), nil
}

//SetTargetTemperature changes the target temperature of a given sensor
func SetTargetTemperature(managementURL string, sensorID int, targetTemperature float32) error {
	value := strconv.FormatFloat(float64(targetTemperature*100), 'f', 0, 32)
	return writeValue(managementURL, sensorID, "SollTemp", value)
}

//SetProgram changes the active week program of the thermostat
func SetProgram(managementURL string, sensorID int, program int) error {
	value := strconv.Itoa(program)
	return writeValue(managementURL, sensorID, "WeekProg", value)
}

//SetMode changes the active operating mode
func SetMode(managementURL string, sensorID int, mode int) error {
	value := strconv.Itoa(mode)
	return writeValue(managementURL, sensorID, "OPMode", value)
}

//GetSensors returns current sensor data for the sensors on the server
func GetSensors(managementURL string, sensorCount int) (sensors []Sensor, err error) {
	//Create request for all values
	req := readRequest{}
	req.Items = make([]readRequestItem, sensorCount*5)
	for i := 0; i < sensorCount; i++ {
		req.Items[i*5+0].Name = fmt.Sprintf("G%v.RaumTemp", i)
		req.Items[i*5+1].Name = fmt.Sprintf("G%v.SollTemp", i)
		req.Items[i*5+2].Name = fmt.Sprintf("G%v.name", i)
		req.Items[i*5+3].Name = fmt.Sprintf("G%v.WeekProg", i)
		req.Items[i*5+4].Name = fmt.Sprintf("G%v.OPmode", i)
	}

	resp, err := readValues(managementURL, req)
	if err != nil {
		return []Sensor{}, err
	}

	//parse response to list of sensors
	var sensorInfoParser = regexp.MustCompile(`^G([0-9]+)\.(.+)$`)
	sensors = make([]Sensor, sensorCount)
	for i := 0; i < len(resp.Items); i++ {
		item := resp.Items[i]

		sensorInfo := sensorInfoParser.FindStringSubmatch(item.Name)
		if len(sensorInfo) > 0 {
			//parse sensor index from name
			sensorIndex, err := strconv.ParseInt(sensorInfo[1], 10, 8)
			if err != nil {
				fmt.Printf("Error parsing sensor index %v\n", sensorInfo[1])
			}
			sensor := &sensors[int(sensorIndex)]

			//try to parse value as float (int)
			var floatValue float32
			intValue, err := strconv.ParseInt(item.Value, 10, 16)
			if err == nil {
				floatValue = float32(intValue) / 100
			}

			valueName := sensorInfo[2]
			sensors[int(sensorIndex)].Id = int(sensorIndex)
			switch valueName {
			case "RaumTemp":
				sensor.RoomTemperature = floatValue
			case "SollTemp":
				sensor.TargetTemperature = floatValue
			case "name":
				sensor.Name = item.Value
			case "WeekProg":
				sensor.Program = int(intValue)
			case "OPmode":
				sensor.Mode = int(intValue)
			default:
				fmt.Printf("Unexpected value name %v\n", valueName)
			}

		} else {
			fmt.Printf("error parsing sensor info name: %v\n", item.Name)
		}
	}

	return sensors, nil
}
