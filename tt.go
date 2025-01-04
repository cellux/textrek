package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var bpm float64 = 120
var nchannels int = 2
var sr int64 = 48000

var steps int = 16
var step float64 = 1 / 4

type SampleBuffer []float64

func NewSampleBuffer() SampleBuffer {
	return make([]float64, 0)
}

func (buf SampleBuffer) Clear() {
	for i := range buf {
		buf[i] = 0
	}
}

type Processor interface {
	Process(t *Track, buf SampleBuffer)
}

type DataLines map[byte]string

type Track struct {
	factory ProcessorFactory
	proc    Processor
	clear   bool
	data    DataLines
	bpm     float64
	step    float64 // length of a step (in beats)
	steps   int     // number of steps in the track
}

func (t *Track) BeatsPerSecond() float64 {
	return t.bpm / 60.0
}

func (t *Track) SamplesPerBeat() float64 {
	return float64(sr) / t.BeatsPerSecond()
}

func (t *Track) SamplesPerStep() int {
	return int(t.SamplesPerBeat() * float64(t.step))
}

func (t *Track) Frames() int {
	return t.SamplesPerStep() * t.steps
}

func (t *Track) Process(buf SampleBuffer) {
	t.proc.Process(t, buf)
}

type Pattern []*Track
type Song []Pattern

type ProcessorFactory func(args string) (Processor, error)

func basicSynthFactory(args string) (Processor, error) {
	return nil, nil
}

var processorFactories = map[string]ProcessorFactory{
	"basic": basicSynthFactory,
}

func parseFloat(s string) (float64, error) {
	slashIndex := strings.IndexByte(s, '/')
	if slashIndex == -1 {
		return strconv.ParseFloat(s, 64)
	}
	nom, err := strconv.ParseFloat(s[:slashIndex], 64)
	if err != nil {
		return 0, err
	}
	denom, err := strconv.ParseFloat(s[slashIndex+1:], 64)
	if err != nil {
		return 0, err
	}
	return nom / denom, nil
}

func processFile(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	var song Song
	var pattern Pattern
	var track *Track
	scanner := bufio.NewScanner(f)
	setGlobalPattern := regexp.MustCompile(`^(bpm|sr|steps|step)\s+(.+)$`)
	setProcessorPattern := regexp.MustCompile(`^([:+])([^:]+)?(?::(.+))?$`)
	setDataPattern := regexp.MustCompile(`^(.)(.+)$`)
	emptyLinePattern := regexp.MustCompile(`^\s+$`)
	for scanner.Scan() {
		line := scanner.Text()
		if line == ">>" {
			song = nil
			pattern = nil
			track = nil
		} else if line == "<<" {
			break
		} else if matches := setGlobalPattern.FindStringSubmatch(line); matches != nil {
			option := matches[1]
			switch option {
			case "bpm":
				if value, err := parseFloat(matches[2]); err != nil {
					return fmt.Errorf("Cannot parse bpm value: %s, %w", matches[2], err)
				} else {
					bpm = value
				}
			case "sr":
				if value, err := strconv.ParseInt(matches[2], 10, 64); err != nil {
					return fmt.Errorf("Cannot parse sr value: %s: %w", matches[2], err)
				} else {
					sr = value
				}
			case "steps":
				if value, err := strconv.ParseInt(matches[2], 10, 64); err != nil {
					return fmt.Errorf("Cannot parse steps value: %s: %w", matches[2], err)
				} else {
					steps = int(value)
				}
			case "step":
				if value, err := parseFloat(matches[2]); err != nil {
					return fmt.Errorf("Cannot parse step value: %s: %w", matches[2], err)
				} else {
					step = value
				}
			}
		} else if matches := setProcessorPattern.FindStringSubmatch(line); matches != nil {
			clear := true
			if matches[1] == "+" {
				clear = false
			}
			name := matches[2]
			if name == "" {
				if track == nil {
					return fmt.Errorf("attempt to reuse a processor which has not been defined")
				}
				args := matches[3]
				if proc, err := track.factory(args); err != nil {
					return fmt.Errorf("cannot instantiate processor %s: %v", name, err)
				} else {
					pattern = append(pattern, track)
					track.proc = proc
					track.data = make(DataLines)
					track.bpm = bpm
					track.step = step
					track.steps = steps
				}
			} else if factory, ok := processorFactories[name]; ok {
				args := matches[3]
				if proc, err := factory(args); err != nil {
					return fmt.Errorf("cannot instantiate processor %s: %v", name, err)
				} else {
					if track != nil {
						pattern = append(pattern, track)
					}
					track = &Track{
						factory: factory,
						proc:    proc,
						clear:   clear,
						data:    make(DataLines),
						bpm:     bpm,
						step:    step,
						steps:   steps,
					}
				}
			} else {
				return fmt.Errorf("unknown processor: %s", name)
			}
		} else if matches := setDataPattern.FindStringSubmatch(line); matches != nil {
			if track == nil {
				return fmt.Errorf("data line without track")
			}
			code := matches[1][0]
			data := matches[2]
			track.data[code] = data
		} else if emptyLinePattern.MatchString(line) {
			if pattern != nil {
				song = append(song, pattern)
				pattern = nil
				track = nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if pattern != nil {
		song = append(song, pattern)
		pattern = nil
		track = nil
	}
	songSamples := NewSampleBuffer()
	writePos := 0
	for _, pattern := range song {
		samples := NewSampleBuffer()
		patternFrames := 0
		for _, track := range pattern {
			if track.clear {
				samples.Clear()
			}
			track.Process(samples)
			trackFrames := track.Frames()
			if trackFrames > patternFrames {
				patternFrames = trackFrames
			}
		}
		songSamples = slices.Grow(songSamples, len(samples))
		for i := 0; i < len(samples); i++ {
			songSamples[writePos+i] += samples[i]
		}
		writePos += patternFrames * nchannels
	}
	filenameExt := filepath.Ext(filename)
	outputFileName := strings.TrimSuffix(filename, filenameExt) + ".wav"
	if err := writeWav(outputFileName, songSamples); err != nil {
		return fmt.Errorf("failed to write %s: %v", outputFileName, err)
	}
	return nil
}

func writeWav(filename string, samples []float64) error {
	bitDepth := 16
	intBuffer := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 2,
			SampleRate:  int(sr),
		},
		Data:           make([]int, len(samples)),
		SourceBitDepth: bitDepth,
	}
	for i := 0; i < len(samples); i++ {
		intBuffer.Data[i] = int(samples[i] * 32767)
	}
	out, err := os.Create(filename)
	if err != nil {
		return err
	}
	e := wav.NewEncoder(out, intBuffer.Format.SampleRate, bitDepth, intBuffer.Format.NumChannels, 1)
	if err := e.Write(intBuffer); err != nil {
		return err
	}
	if err := e.Close(); err != nil {
		return err
	}
	return nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"textrek - A music compiler\n\nUsage: textrek [options] <file>\n\n")
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
	} else {
		for _, filename := range flag.Args() {
			if err := processFile(filename); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to process file %s: %v", filename, err)
				os.Exit(1)
			}
		}
	}
}
