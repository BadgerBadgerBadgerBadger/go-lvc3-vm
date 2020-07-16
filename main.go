package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/mpatraw/gocurse/curses"
)

const uint16Max = 65535

const (
	R0 = iota
	R1
	R2
	R3
	R4
	R5
	R6
	R7
	RPc
	RCond
	RCount
)

const (
	MrKbsr = 0xFE00 /* keyboard status */
	MrKbdr = 0xFE02 /* keyboard data */
)

const (
	OpBreak = iota
	OpAdd
	OpLoad
	OpStore
	OpJumpRegister
	OpAnd
	OpLoadRegister
	OpStoreRegister
	OpRti
	OpNot
	OpLoadIndirect
	OpStoreIndirect
	OpJump
	OpRes
	OpLoadEffectiveAddress
	OpTrap
)

const (
	TrapGetC  = 0x20 /* get character from keyboard */
	TrapOut   = 0x21 /* output a character */
	TrapPutS  = 0x22 /* output a word string */
	TrapIn    = 0x23 /* input a string */
	TrapPutSP = 0x24 /* output a byte string */
	TrapHalt  = 0x25 /* halt the program */
)

const (
	FlagPositive = 1 << 0
	FlagZero     = 1 << 1
	FlagNegative = 1 << 2
)

var memory [uint16Max]uint16
var registers [RCount]uint16

var reader *bufio.Reader
var screen *curses.Window
var shutdownCh = make(chan struct{})

func main() {

	argsWithoutProg := os.Args[1:]
	fmt.Printf("Input image: %+v\n", argsWithoutProg[0])

	readImage(argsWithoutProg[0])
	fmt.Println("Image read.")

	const PcStart = 0x3000
	registers[RPc] = PcStart

	running := true
	reader = bufio.NewReader(os.Stdin)

	// Initscr() initializes the terminal in curses mode.
	screen, _ = curses.Initscr()
	// Endwin must be called when done.
	defer curses.Endwin()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		close(shutdownCh)
	}()
	fmt.Println("Interrupt registered.")

	for running {

		select {
		case <-shutdownCh:
			return
			// Other cases might be listed here..
		default:
		}

		instruction := memRead(registers[RPc])
		registers[RPc]++
		opCode := instruction >> 12

		switch opCode {
		case OpBreak:

			isNegative := ((instruction >> 11) & 0x1) == 1
			isZero := ((instruction >> 10) & 0x1) == 1
			isPositive := ((instruction >> 9) & 0x1) == 1

			isSet := (isNegative && registers[RCond] == FlagNegative) || (isZero && registers[RCond] == FlagZero) ||
				(isPositive && registers[RCond] == FlagPositive)

			if isSet {
				registers[RPc] = pcPlusOffset(registers[RPc], instruction&0x1fff, 9)
			}

		case OpAdd:

			destRegister := (instruction >> 9) & 0x7
			firstOperand := (instruction >> 6) & 0x7

			immFlag := (instruction >> 5) & 0x1

			if immFlag == 1 {
				imm5 := signExtend(instruction&0x1F, 5)
				registers[destRegister] = registers[firstOperand] + imm5
			} else {
				secondOperand := instruction & 0x7
				registers[destRegister] = registers[firstOperand] + registers[secondOperand]
			}

			updateFlags(destRegister)

		case OpLoad:

			destRegister := (instruction >> 9) & 0x7
			pcOffset9 := signExtend(instruction&0x1fff, 9)

			registers[destRegister] = memRead(registers[RPc] + pcOffset9)

			updateFlags(destRegister)

		case OpStore:

			sourceRegister := (instruction >> 9) & 0x7
			pcOffset9 := signExtend(instruction&0x1fff, 9)

			memWrite(registers[RPc+pcOffset9], registers[sourceRegister])

		case OpJumpRegister:

			registers[R7] = registers[RPc]
			bit11Set := ((instruction >> 11) & 0x1) == 1

			if bit11Set {

				pcOffset11 := signExtend(instruction&0x7ff, 11)
				registers[RPc] = registers[RPc] + pcOffset11

			} else {

				baseRegister := (instruction >> 6) & 0x7
				registers[RPc] = registers[baseRegister]
			}

		case OpAnd:

			destRegister := (instruction >> 9) & 0x7
			firstOperand := (instruction >> 6) & 0x7

			immFlag := (instruction >> 5) & 0x1

			if immFlag == 1 {
				imm5 := signExtend(instruction&0x1F, 5)
				registers[destRegister] = registers[firstOperand] & imm5
			} else {
				secondOperand := instruction & 0x7
				registers[destRegister] = registers[firstOperand] & registers[secondOperand]
			}

			updateFlags(destRegister)

		case OpLoadRegister:

			destRegister := (instruction >> 9) & 0x7
			baseRegister := (instruction >> 6) & 0x7
			pcOffset6 := signExtend(instruction&0x3f, 6)

			registers[destRegister] = memRead(registers[baseRegister] + pcOffset6)

			updateFlags(destRegister)

		case OpStoreRegister:

			sourceRegister := (instruction >> 9) & 0x7
			baseRegister := (instruction >> 6) & 0x7
			pcOffset6 := signExtend(instruction&0x3f, 6)

			memWrite(registers[baseRegister]+pcOffset6, registers[sourceRegister])

		case OpRti:
			panic("not implemented")

		case OpNot:

			destRegister := (instruction >> 9) & 0x7
			sourceRegister := (instruction >> 6) & 0x7

			registers[destRegister] = ^registers[sourceRegister]

			updateFlags(destRegister)

		case OpLoadIndirect:

			destRegister := (instruction >> 9) & 0x7
			pcOffset9 := signExtend(instruction&0x1fff, 9)

			registers[destRegister] = memRead(memRead(registers[RPc] + pcOffset9))

			updateFlags(destRegister)

		case OpStoreIndirect:

			sourceRegister := (instruction >> 9) & 0x7
			pcOffset9 := signExtend(instruction & 0x1fff, 9)

			memWrite(memRead(registers[RPc+pcOffset9]), registers[sourceRegister])

		case OpJump:

			dest := (instruction >> 6) & 0x7
			registers[RPc] = registers[dest]

		case OpRes:
			panic("not implemented")

		case OpLoadEffectiveAddress:

			destRegister := (instruction >> 9) & 0x7
			pcOffset9 := signExtend(instruction&0x1fff, 9)

			registers[destRegister] = registers[RPc] + pcOffset9

			updateFlags(destRegister)

		case OpTrap:

			trapCode := instruction & 0xFF

			switch trapCode {
			case TrapGetC:

				registers[R0] = getChar()

			case TrapOut:

				print(string(registers[R0] & 0xff))

			case TrapPutS:

				loc := registers[R0]

				for {
					char := memRead(loc)

					if char == 0 {
						break
					}

					print(string(byte(char)))
					loc++
				}

			case TrapIn:

				print("Enter a character: ")

				registers[R0] = getChar()

			case TrapPutSP:

				loc := registers[R0]

				for {

					read := memRead(loc)
					first := read & 0xff

					if first == 0 {
						break
					}

					print(string(byte(first)))

					second := read >> 8

					if second == 0 {
						break
					}

					print(string(byte(second)))

					loc++
				}

			case TrapHalt:

				print("halt")
				running = false
			}
		}
	}
}

func memRead(address uint16) uint16 {

	if address == MrKbsr {
		if checkKey() {
			memory[MrKbsr] = 0x1 << 15
			memory[MrKbdr] = getChar()
		}
	} else {
		memory[MrKbsr] = 0
	}

	return memory[address]
}

func memWrite(address uint16, value uint16) {
	memory[address] = value
}

func signExtend(num uint16, bitCount uint) uint16 {

	if (num >> (bitCount - 1)) & 1 == 1 {
		return num | (0xffff << bitCount)
	}

	return num
}

func checkKey() bool {
	ch := screen.Getch()
	return ch != 0
}

func updateFlags(register uint16) {

	if registers[register] == 0 {
		registers[RCond] = FlagZero
	} else if registers[register]>>15 == 1 {
		registers[RCond] = FlagNegative
	} else {
		registers[RCond] = FlagPositive
	}
}

func readImage(path string) {

	file, err := os.Open(path)
	defer file.Close()
	checkErr(err)

	readImageFile(file)
}

func readImageFile(file *os.File) {

	origin, _ := readUint16(file)
	fmt.Printf("Origin: %x\n", origin)

	for i := origin; i < (uint16Max - origin); i++ {
		val, err := readUint16(file)

		if err != nil && err == io.EOF {
			break
		}

		memory[i] = val
	}
}

func readUint16(file *os.File) (uint16, error) {

	value := make([]byte, 2)
	_, err := file.Read(value[:])

	if err != nil {
		return 0, err
	}

	reader := bytes.NewReader(value[:])

	var finalVal uint16
	err = binary.Read(reader, binary.BigEndian, &finalVal)
	checkErr(err)

	return finalVal, nil
}

func getChar() uint16 {
	char, _, err := reader.ReadRune()
	checkErr(err)

	return uint16(byte(char))
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

func pcPlusOffset(valInPc uint16, num uint16, bitCount uint) uint16 {

	signedNum := int16(num)

	if (num >> (bitCount - 1)) & 1 == 1 {
		signedNum |= (0xffff << bitCount)
	}

	return uint16(int16(valInPc) + signedNum)
}
