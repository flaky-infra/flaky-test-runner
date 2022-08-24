package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/streadway/amqp"
)

type TestEndMessage struct {
	TestFailuresList      []TestFailures `json:"testFailures" xml:"testcase"`
	IsLastConfig          bool           `json:"isLastConfig" xml:"isLastConfig"`
	ProjectId             string         `json:"projectId" xml:"projectId"`
	TestRunId             string         `json:"testRunId" xml:"testRunId"`
	ConfigFile            string         `json:"configFile" xml:"configFile"`
	ScenarioConfiguration string         `json:"scenarioConfiguration" xml:"scenarioConfiguration"`
	TestMethodName        string         `json:"testMethodName" xml:"testMethodName"`
	ExecutionLog          string         `json:"executionLog" xml:"executionLog"`
	NumberOfScenarios     string         `json:"numberOfScenarios" xml:"numberOfScenarios"`
	Duration              string         `json:"duration" xml:"duration"`
}

type TestFailures struct {
	XMLName   xml.Name   `json:"-" xml:"testfailures"`
	Text      string     `json:"-" xml:",chardata"`
	Duration  string     `json:"duration" xml:"duration,attr"`
	TestCases []TestCase `json:"testCases" xml:"testcase"`
}

type TestCase struct {
	Text        string `json:"-" xml:",chardata"`
	Classname   string `json:"classname" xml:"classname,attr"`
	DisplayName string `json:"displayName" xml:"displayName,attr"`
	Message     string `json:"message" xml:"message,attr"`
	Name        string `json:"name" xml:"name,attr"`
	Stacktrace  string `json:"stacktrace" xml:"stacktrace,attr"`
}

func main() {
	applicationPath := os.Getenv("FLAKY_APP_PATH")
	testRunCmd := os.Getenv("FLAKY_TEST_RUN_CMD")
	os.Chdir(applicationPath)

	diskInputLoad := "102400"
	// diskInputLoad := os.Getenv("FLAKY_DISK_INPUT_LOAD")
	_, inputFileGeneration := exec.Command("bash", "-c", fmt.Sprintf("dd if=/dev/zero of=/tmp/input.tmp bs=4k count=%s && sync", diskInputLoad)).Output()
	if inputFileGeneration != nil {
		fmt.Println(inputFileGeneration.Error())
	}

	startTime := time.Now()
	commandOutput, commandErr := exec.Command(testRunCmd).Output()
	if commandErr != nil {
		fmt.Println(commandErr)
		if exitError, ok := commandErr.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			exitCode := ws.ExitStatus()
			fmt.Println(exitCode)
		}
		os.Exit(1)
	}
	elapsedTime := time.Since(startTime)

	testFailuresList := make([]TestFailures, 0)
	err := filepath.Walk("./FLAKY_TEST_OUTPUT/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err)
			return err
		}
		fileExtension := filepath.Ext(path)
		if fileExtension != ".xml" {
			return nil
		}
		xmlFile, err := os.Open(path)
		if err != nil {
			fmt.Println(err)
		}
		defer xmlFile.Close()
		byteValue, _ := ioutil.ReadAll(xmlFile)

		var testFailures TestFailures
		xml.Unmarshal(byteValue, &testFailures)
		testFailuresList = append(testFailuresList, testFailures)
		return nil
	})
	if err != nil {
		log.Println("Walk")
		log.Fatal(err)
	}

	log.Println("Logs collected")

	isLastConfig, _ := strconv.ParseBool(os.Getenv("FLAKY_IS_LAST_CONFIG"))
	testEndMessage := TestEndMessage{TestFailuresList: testFailuresList, IsLastConfig: isLastConfig, ProjectId: os.Getenv("FLAKY_PROJECT_ID"), TestRunId: os.Getenv("FLAKY_TEST_RUN_ID"), ConfigFile: os.Getenv("FLAKY_CONFIG_FILE"), ScenarioConfiguration: os.Getenv("FLAKY_CONFIGURATION"), TestMethodName: os.Getenv("FLAKY_TEST_ID"), ExecutionLog: string(commandOutput), NumberOfScenarios: os.Getenv("FLAKY_SCENARIOS_NUMBER"), Duration: strconv.FormatInt(elapsedTime.Milliseconds(), 10)}

	log.Println("TestEndMessage created")

	testEndMessageJson, err := json.Marshal(testEndMessage)
	if err != nil {
		log.Println("Marshal")
		log.Fatal(err)
	}

	log.Println("TestEndMessageJson created")

	rabbitMqUri := strings.TrimSpace(os.Getenv("FLAKY_RABBITMQ_URI"))
	if rabbitMqUri == "" {
		log.Fatal("Error: FLAKY_RABBITMQ_URI must be set.")
	}
	rabbitMqUsername := strings.TrimSpace(os.Getenv("FLAKY_RABBITMQ_USERNAME"))
	if rabbitMqUsername == "" {
		log.Fatal("Error: FLAKY_RABBITMQ_USERNAME must be set.")
	}
	rabbitMqPassword := strings.TrimSpace(os.Getenv("FLAKY_RABBITMQ_PASSWORD"))
	if rabbitMqPassword == "" {
		log.Fatal("Error: FLAKY_RABBITMQ_PASSWORD must be set.")
	}

	connectRabbitMQ, err := amqp.Dial(strings.Replace(strings.Replace(rabbitMqUri, "<username>", rabbitMqUsername, 1), "<password>", rabbitMqPassword, 1))
	if err != nil {
		log.Println("RabbitMQ connection")
		log.Fatal(err)
	}
	defer connectRabbitMQ.Close()

	channelRabbitMQ, err := connectRabbitMQ.Channel()
	if err != nil {
		log.Println("Channel")
		log.Fatal(err)
	}
	defer channelRabbitMQ.Close()

	log.Println("Rabbit Connected")

	err = channelRabbitMQ.ExchangeDeclare(
		"flaky-topic-exchange", // name
		"topic",                // type
		false,                  // durable
		false,                  // auto-deleted
		false,                  // internal
		false,                  // no-wait
		nil,                    // arguments
	)
	if err != nil {
		log.Println("ExchangeDeclare")
		log.Fatal(err)
	}

	err = channelRabbitMQ.Publish(
		"flaky-topic-exchange", // exchange
		"project.test.end",     // routing key
		false,                  // mandatory
		false,                  // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        testEndMessageJson,
		})

	if err != nil {
		log.Println("Publish")
		log.Fatal(err)
	}

	log.Println("Correctly sent")

	// if len(testFailures.TestCases) == 0 {
	// 	fmt.Println("No test failed!")
	// } else {
	// 	fmt.Println("Following test present failures:")
	// }
	// for _, testCase := range testFailures.TestCases {
	// 	fmt.Printf("%s: %s\n", testCase.Name, testCase.Message)
	// }
}
