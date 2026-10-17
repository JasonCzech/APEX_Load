package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import ()

type AIGuardrailReport = importreview.AIGuardrailReport

type AIGuardrailItem = importreview.AIGuardrailItem

var buildAIGuardrailReport = importreview.BuildAIGuardrailReport

var aiGuardrailItem = importreview.AiGuardrailItem

var isAIGeneratedDraft = importreview.IsAIGeneratedDraft

var hasAIProvenance = importreview.HasAIProvenance

var preservedBool = importreview.PreservedBool

var preservedString = importreview.PreservedString
