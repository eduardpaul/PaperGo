package connections

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxTopicBytes bounds topics and topic filters.
const MaxTopicBytes = 256

// checkFilter says why filter is not an MQTT topic filter PaperGo serves:
// '+' takes a whole level, '#' a whole last level, and topics that start
// with '$' (the broker's own, and shared subscriptions) are never reached.
func checkFilter(filter string) error {
	if filter == "" || len(filter) > MaxTopicBytes || !utf8.ValidString(filter) || strings.ContainsRune(filter, 0) {
		return errors.New("a topic filter has 1 to 256 bytes of UTF-8")
	}
	if strings.HasPrefix(filter, "$") {
		return errors.New("topics starting with $ belong to the broker")
	}
	levels := strings.Split(filter, "/")
	for i, level := range levels {
		if strings.Contains(level, "+") && level != "+" {
			return errors.New("+ stands for a whole level, as in home/+/temperature")
		}
		if strings.Contains(level, "#") && (level != "#" || i != len(levels)-1) {
			return errors.New("# stands for the last levels, as in home/#")
		}
	}
	return nil
}

// checkTopic says why topic is not a topic a message may be published to:
// a concrete topic, without wildcards.
func checkTopic(topic string) error {
	if err := checkFilter(topic); err != nil {
		return err
	}
	if strings.ContainsAny(topic, "+#") {
		return errors.New("publish to one topic, without + or #")
	}
	return nil
}

// topicMatches reports whether filter matches topic. '#' also matches its
// parent level: home/# matches home.
func topicMatches(filter, topic string) bool {
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, level := range f {
		if level == "#" {
			return true
		}
		if i >= len(t) {
			return false
		}
		if level != "+" && level != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}

// filterCovers reports whether every topic sub matches, outer matches too:
// a subscription inside a channel.
func filterCovers(outer, sub string) bool {
	o, s := strings.Split(outer, "/"), strings.Split(sub, "/")
	for i := 0; ; i++ {
		if i < len(o) && o[i] == "#" {
			return true
		}
		if i == len(s) {
			return i == len(o) || i == len(o)-1 && o[i] == "#"
		}
		if i == len(o) || s[i] == "#" {
			return false
		}
		if o[i] != "+" && (s[i] == "+" || o[i] != s[i]) {
			return false
		}
	}
}

// filtersOverlap reports whether some topic matches both filters.
func filtersOverlap(a, b string) bool {
	x, y := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; ; i++ {
		if i < len(x) && x[i] == "#" || i < len(y) && y[i] == "#" {
			return true
		}
		if i == len(x) || i == len(y) {
			// One is done: the other matches no more levels unless it ends
			// in #, which the check above would have seen.
			return i == len(x) && i == len(y)
		}
		if x[i] != "+" && y[i] != "+" && x[i] != y[i] {
			return false
		}
	}
}
