/*
 * errors.go - File which contains common error handling code for fscrypt
 * commands. This includes handling for bad usage, invalid commands, and errors
 * from the other packages
 *
 * Copyright 2017 Google Inc.
 * Author: Joe Richey (joerichey@google.com)
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not
 * use this file except in compliance with the License. You may obtain a copy of
 * the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
 * License for the specific language governing permissions and limitations under
 * the License.
 */

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/google/fscrypt/actions"
	"github.com/google/fscrypt/filesystem"
	"github.com/google/fscrypt/metadata"
	"github.com/google/fscrypt/util"
)

// failureExitCode is the value fscrypt will return on failure.
const failureExitCode = 1

// Various errors used for the top level user interface
var (
	ErrCanceled           = errors.New("operation canceled")
	ErrNoDesctructiveOps  = errors.New("operation would be destructive")
	ErrMaxPassphrase      = util.SystemError("max passphrase length exceeded")
	ErrInvalidSource      = errors.New("invalid source type")
	ErrPassphraseMismatch = errors.New("entered passphrases do not match")
	ErrSpecifyProtector   = errors.New("multiple protectors available")
	ErrWrongKey           = errors.New("incorrect key provided")
	ErrSpecifyKeyFile     = errors.New("no key file specified")
	ErrKeyFileLength      = errors.Errorf("key file must be %d bytes", metadata.PolicyKeyLen)
	ErrAllLoadsFailed     = errors.New("could not load any protectors")
	ErrMustBeRoot         = errors.New("this command must be run as root")
	ErrPolicyUnlocked     = errors.New("this file or directory is already unlocked")
	ErrBadOwners          = errors.New("you do not own this directory")
	ErrNotEmptyDir        = errors.New("not an empty directory")
	ErrNotPassphrase      = errors.New("protector does not use a passphrase")
	ErrUnknownUser        = errors.New("unknown user")
)

var loadHelpText = fmt.Sprintf("You may need to mount a linked filesystem. Run with %s for more information.", shortDisplay(verboseFlag))

// getFullName returns the full name of the application or command being used.
func getFullName(c *cli.Context) string {
	if c.Command.HelpName != "" {
		return c.Command.HelpName
	}
	return c.App.HelpName
}

// getErrorSuggestions returns a string containing suggestions about how to fix
// an error. If no suggestion is necessary or available, return empty string.
func getErrorSuggestions(err error) string {
	switch errors.Cause(err) {
	case filesystem.ErrNotSetup:
		return fmt.Sprintf(`Run "fscrypt setup %s" to use fscrypt on this filesystem.`, mountpointArg)
	case metadata.ErrEncryptionNotSupported:
		return `Encryption for this type of filesystem is not supported
			on this kernel version.`
	case metadata.ErrEncryptionNotEnabled:
		return `Encryption is either disabled in the kernel config, or
			needs to be enabled for this filesystem. See the
			documentation on how to enable encryption on ext4
			systems (and the risks of doing so).`
	case actions.ErrBadConfigFile:
		return `Run "sudo fscrypt setup" to recreate the file.`
	case actions.ErrNoConfigFile:
		return `Run "sudo fscrypt setup" to create the file.`
	case actions.ErrMissingPolicyMetadata:
		return `This file or directory has either been encrypted with
			another tool (such as e4crypt) or the corresponding
			filesystem metadata has been deleted.`
	case actions.ErrPolicyMetadataMismatch:
		return `The metadata for this encrypted directory is in an
			inconsistent state. This most likely means the filesystem
			metadata is corrupted.`
	case actions.ErrMissingProtectorName:
		return fmt.Sprintf("Use %s to specify a protector name.", shortDisplay(nameFlag))
	case ErrNoDesctructiveOps:
		return fmt.Sprintf("Use %s to automatically run destructive operations.", shortDisplay(forceFlag))
	case ErrSpecifyProtector:
		return fmt.Sprintf("Use %s to specify a protector.", shortDisplay(protectorFlag))
	case ErrSpecifyKeyFile:
		return fmt.Sprintf("Use %s to specify a key file.", shortDisplay(keyFileFlag))
	case ErrBadOwners:
		return `Encryption can only be setup on directories you own,
			even if you have write permission for the directory.`
	case ErrNotEmptyDir:
		return `Encryption can only be setup on empty directories; files
			cannot be encrypted in-place. Instead, encrypt an empty
			directory, copy the files into that encrypted directory,
			and securely delete the originals with "shred".`
	case ErrAllLoadsFailed:
		return loadHelpText
	default:
		return ""
	}
}

// newExitError creates a new error for a given context and normal error. The
// returned error prepends the name of the relevant command and will make
// fscrypt return a non-zero exit value.
func newExitError(c *cli.Context, err error) error {
	// Prepend the full name and append suggestions (if any)
	fullNamePrefix := getFullName(c) + ": "
	message := fullNamePrefix + wrapText(err.Error(), utf8.RuneCountInString(fullNamePrefix))

	if suggestion := getErrorSuggestions(err); suggestion != "" {
		message += "\n\n" + wrapText(suggestion, 0)
	}

	return cli.NewExitError(message, failureExitCode)
}

// usageError implements cli.ExitCoder to will print the usage and the return a
// non-zero value. This error should be used when a command is used incorrectly.
type usageError struct {
	c       *cli.Context
	message string
}

func (u *usageError) Error() string {
	return fmt.Sprintf("%s: %s", getFullName(u.c), u.message)
}

// We get the help to print after the error by having it run right before the
// application exits. This is very nasty, but there isn't a better way to do it
// with the constraints of urfave/cli.
func (u *usageError) ExitCode() int {
	// Redirect help output to a buffer, so we can customize it.
	buf := new(bytes.Buffer)
	oldWriter := u.c.App.Writer
	u.c.App.Writer = buf

	// Get the appropriate help
	if getFullName(u.c) == filepath.Base(os.Args[0]) {
		cli.ShowAppHelp(u.c)
	} else {
		cli.ShowCommandHelp(u.c, u.c.Command.Name)
	}

	// Remove first line from help and print it out
	buf.ReadBytes('\n')
	buf.WriteTo(oldWriter)
	u.c.App.Writer = oldWriter
	return failureExitCode
}

// expectedArgsErr creates a usage error for the incorrect number of arguments
// being specified. atMost should be true only if any number of arguments from 0
// to expectedArgs would be acceptable.
func expectedArgsErr(c *cli.Context, expectedArgs int, atMost bool) error {
	message := "expected "
	if atMost {
		message += "at most "
	}
	message += fmt.Sprintf("%s, got %s",
		pluralize(expectedArgs, "argument"), pluralize(c.NArg(), "argument"))
	return &usageError{c, message}
}

// onUsageError is a function handler for the application and each command.
func onUsageError(c *cli.Context, err error, _ bool) error {
	return &usageError{c, err.Error()}
}

// checkRequiredFlags makes sure that all of the specified string flags have
// been given nonempty values. Returns a usage error on failure.
func checkRequiredFlags(c *cli.Context, flags []*stringFlag) error {
	for _, flag := range flags {
		if flag.Value == "" {
			message := fmt.Sprintf("required flag %s not provided", shortDisplay(flag))
			return &usageError{c, message}
		}
	}
	return nil
}
