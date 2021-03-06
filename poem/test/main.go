package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/fumin/ntm"
	"github.com/fumin/ntm/poem"
)

var (
	weightsFile = flag.String("weightsFile", "", "trained weights in JSON")
	c, gen      = setup()
)

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func sayHello(w http.ResponseWriter, r *http.Request) {
	message := r.URL.Path
	message = strings.TrimPrefix(message, "/")
	message = "Hello " + message
	w.Write([]byte(message))
	log.Printf(message)
}

func doInfer(w http.ResponseWriter, r *http.Request) {
	heads := r.URL.Path
	heads = strings.TrimPrefix(heads, "/infer/")
	log.Printf("Heads: %s", heads)
	//for k := 0; k < len([]rune(line))/4; k++ {
	//	//_ = "breakpoint"
	//	for j := k * 4; j < k*4+4; j++ {
	//		p[j%4][0] = string([]rune(line)[j])
	//	}
	rand.Seed(15)
	_ = "breakpoint"
	length := len([]rune(heads))
	p := make([][]string, length)
	log.Printf("p: %s", p)
	for i := range p {
		p[i] = make([]string, 5)
	}
	for i := 0; i < len([]rune(heads)); i++ {
		p[i][0] = string([]rune(heads)[i])
		for j := 1; j < 5; j++ {
			p[i][j] = ""
		}
	}
	pred := predict(c, p, gen)
	result := showPrediction(pred, gen, p)
	w.Write([]byte(result))
}

func main() {
	http.HandleFunc("/", sayHello)
	http.HandleFunc("/infer/", doInfer)
	log.Printf("Starting server on localhost:8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}

func setup() (ntm.Controller, *poem.Generator) {
	flag.Parse()
	gen, err := poem.NewGenerator("data/quantangshi3000.int")
	if err != nil {
		log.Fatalf("%v", err)
	}
	h1Size := 512
	numHeads := 8
	n := 128
	m := 32
	c := ntm.NewEmptyController1(gen.InputSize(), gen.OutputSize(), h1Size, numHeads, n, m)
	assignWeights(c)
	return c, gen
}

func showPrediction(pred [][]float64, gen *poem.Generator, oripoem [][]string) string {
	ps := make([]string, len(pred))

	// Prepare a slice representation of the poem constraints.
	i := len(pred)/2 + 1
	poema := make([]string, len(pred))
	for _, line := range oripoem {
		for _, c := range line {
			if c != "" {
				poema[i] = c
			}
			i++
		}
		poema[i] = poem.CharLinefeed
		i++
	}

	// Determine the final characters from the predicted probability densities, with the following requirements:
	//   * The choosen character is the same as the input constraints.
	//   * The choosen characters are unique among themselves.
	res := make([][]poem.Char, len(pred))
	for i, p := range pred {
		if i < len(pred)/2+1 {
			ps[i] = ""
		} else if poema[i] != "" {
			ps[i] = poema[i]
		} else {
			sorted := gen.SortOutput(p)
			for _, c := range sorted {
				if c.S == poem.CharUnknown || c.S == poem.CharLinefeed {
					continue
				}
				var dup = false
				for _, psc := range ps {
					if psc == c.S {
						dup = true
						break
					}
				}
				if !dup {
					ps[i] = c.S
					break
				}
			}
		}

		res[i] = gen.SortOutput(p)[0:5]
	}

	// Print the probability densities.
	for i, chars := range res {
		log.Printf("%s -> %v", ps[i], chars)
		if i == len(res)/2 {
			log.Printf("-------------")
		}
	}

	// Print the final generated poem.
	s := ""
	for _, c := range ps[len(ps)/2+1:] {
		if c == poem.CharLinefeed {
			s += "\n"
		} else {
			s += c
		}
	}
	log.Printf(s)
	return s
}

func predict(c ntm.Controller, shi [][]string, gen *poem.Generator) [][]float64 {
	machine := ntm.MakeEmptyNTM(c)

	// Feed the poem constraints into the NTM.
	numChar := 0
	output := make([][]float64, 0)
	for _, line := range shi {
		for _, s := range line {
			numChar++
			input := vecFromString(s, gen)
			machine, output = forward(machine, input, output)
		}
		numChar++
		input := gen.Linefeed()
		machine, output = forward(machine, input, output)
	}
	input := gen.EndOfPoem()
	machine, output = forward(machine, input, output)

	input = make([]float64, gen.InputSize())
	machine, output = forward(machine, input, output)

	// Follow the predictions of the NTM.
	i := 1
	for _, line := range shi {
		for _, s := range line {
			if s != "" {
				input = vecFromString(s, gen)
			} else {
				input, _ = sample(output[len(output)-1], gen)
			}
			machine, output = forward(machine, input, output)
			i++
		}

		if i >= numChar {
			break
		}
		input, _ = sample(output[len(output)-1], gen)
		machine, output = forward(machine, input, output)
		i++
	}

	return output
}

func sample(output []float64, gen *poem.Generator) ([]float64, int) {
	var characterIndex int
	r := rand.Float64()
	var sum float64
	for i, v := range output {
		sum += v
		if sum >= r {
			characterIndex = i
			break
		}
	}

	input := make([]float64, gen.InputSize())
	input[characterIndex] = 1
	return input, characterIndex
}

func vecFromString(s string, g *poem.Generator) []float64 {
	v := make([]float64, g.InputSize())
	c, ok := g.Dataset.Chars[s]
	if !ok {
		return v
	}
	v[c] = 1
	return v
}

func forward(machine *ntm.NTM, input []float64, output [][]float64) (*ntm.NTM, [][]float64) {
	newMachine := ntm.NewNTM(machine, input)

	// Compute the multinomial output by faking a dummy model.
	model := &ntm.MultinomialModel{Y: []int{0}}
	model.Model(0, newMachine.Controller.YVal(), newMachine.Controller.YGrad())

	output = append(output, newMachine.Controller.YVal())
	return newMachine, output
}

func assignWeights(c ntm.Controller) {
	if *weightsFile == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	f, err := os.Open(*weightsFile)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer f.Close()
	ws := c.WeightsVal()
	if err := json.NewDecoder(f).Decode(&ws); err != nil {
		log.Fatalf("%v", err)
	}
}
