package contractdiff

import "slices"

// Rule IDs are stable: a conf's diff.accept entries name them. A rule's severity can depend
// on the change's direction or on what the old contract planned, so the ID names the change,
// not its severity. `_NO_DELETE` marks a removal that breaks callers unless it was planned.
const (
	// The document.

	RuleRootRenamed                = "ROOT_RENAMED"
	RuleErrorsChanged              = "ERRORS_CHANGED"
	RuleMulticallNoDelete          = "MULTICALL_NO_DELETE"
	RuleMulticallChanged           = "MULTICALL_CHANGED"
	RuleMulticallAdded             = "MULTICALL_ADDED"
	RuleTopicRemoved               = "TOPIC_REMOVED"
	RuleTopicAdded                 = "TOPIC_ADDED"
	RuleCompletionEnvRemoved       = "COMPLETION_ENV_REMOVED"
	RuleCompletionEnvChanged       = "COMPLETION_ENV_CHANGED"
	RuleCompletionEnvAdded         = "COMPLETION_ENV_ADDED"
	RuleResponseFilesAdded         = "RESPONSE_FILES_ADDED"
	RuleResponseFilesPrefixChanged = "RESPONSE_FILES_PREFIX_CHANGED"
	RuleResponseFilesRemoved       = "RESPONSE_FILES_REMOVED"

	// Commands.

	RuleCommandNoDelete           = "COMMAND_NO_DELETE"
	RuleCommandReplaced           = "COMMAND_REPLACED"
	RuleCommandRenamedAliasKept   = "COMMAND_RENAMED_ALIAS_KEPT"
	RuleCommandAdded              = "COMMAND_ADDED"
	RuleCommandHidden             = "COMMAND_HIDDEN"
	RuleCommandUnhidden           = "COMMAND_UNHIDDEN"
	RuleAliasNoDelete             = "ALIAS_NO_DELETE"
	RuleAliasAdded                = "ALIAS_ADDED"
	RuleHiddenAliasNoDelete       = "HIDDEN_ALIAS_NO_DELETE"
	RuleHiddenAliasAdded          = "HIDDEN_ALIAS_ADDED"
	RuleAliasMoved                = "ALIAS_MOVED"
	RulePluginNoDelete            = "PLUGIN_NO_DELETE"
	RulePluginAdded               = "PLUGIN_ADDED"
	RuleOptionsFirstChanged       = "OPTIONS_FIRST_CHANGED"
	RuleCommandPassthroughChanged = "COMMAND_PASSTHROUGH_CHANGED"
	RuleDigitFlagAdded            = "DIGIT_FLAG_ADDED"
	RuleFlagGroupAdded            = "FLAG_GROUP_ADDED"
	RuleFlagGroupRemoved          = "FLAG_GROUP_REMOVED"
	RuleFlagGroupTightened        = "FLAG_GROUP_TIGHTENED"
	RuleFlagGroupLoosened         = "FLAG_GROUP_LOOSENED"
	RuleFlagDependencyAdded       = "FLAG_DEPENDENCY_ADDED"
	RuleFlagDependencyRemoved     = "FLAG_DEPENDENCY_REMOVED"
	RuleFlagDependencyTightened   = "FLAG_DEPENDENCY_TIGHTENED"
	RuleFlagDependencyLoosened    = "FLAG_DEPENDENCY_LOOSENED"
	RuleConfigFileNoDelete        = "CONFIG_FILE_NO_DELETE"
	RuleConfigFileMoved           = "CONFIG_FILE_MOVED"
	RuleConfigFileAsChanged       = "CONFIG_FILE_AS_CHANGED"
	RuleConfigFileAdded           = "CONFIG_FILE_ADDED"
	RulePluginDiscoveryNoDelete   = "PLUGIN_DISCOVERY_NO_DELETE"
	RulePluginDiscoveryChanged    = "PLUGIN_DISCOVERY_PREFIX_CHANGED"
	RulePluginDiscoveryAdded      = "PLUGIN_DISCOVERY_ADDED"
	RuleStdinAdded                = "STDIN_ADDED"
	RuleStdinNoDelete             = "STDIN_NO_DELETE"
	RuleStdinFormatChanged        = "STDIN_FORMAT_CHANGED"
	RuleStdinRequiredAdded        = "STDIN_REQUIRED_ADDED"
	RuleStdinRequiredRemoved      = "STDIN_REQUIRED_REMOVED"
	RuleStdinTypeChanged          = "STDIN_TYPE_CHANGED"
	RuleStdinUnlessChanged        = "STDIN_UNLESS_ARGUMENT_CHANGED"
	RuleStdinSeparatorChanged     = "STDIN_SEPARATOR_CHANGED"
	RuleOutputNoDelete            = "OUTPUT_NO_DELETE"
	RuleOutputAdded               = "OUTPUT_ADDED"
	RuleOutputStreamChanged       = "OUTPUT_STREAM_CHANGED"
	RuleExitStatusNoDelete        = "EXIT_STATUS_NO_DELETE"
	RuleExitStatusAdded           = "EXIT_STATUS_ADDED"
	RuleExitSummaryChanged        = "EXIT_SUMMARY_CHANGED"
	RuleExitNameChanged           = "EXIT_NAME_CHANGED"
	RuleExitNameAdded             = "EXIT_NAME_ADDED"
	RuleExitRetryableRemoved      = "EXIT_RETRYABLE_REMOVED"
	RuleExitRetryableAdded        = "EXIT_RETRYABLE_ADDED"

	// Any command or input.

	RuleDeprecationAdded   = "DEPRECATION_ADDED"
	RuleDeprecationChanged = "DEPRECATION_CHANGED"
	RuleDeprecationRemoved = "DEPRECATION_REMOVED"
	RuleLifecycleChanged   = "LIFECYCLE_CHANGED"
	RuleReplacedByChanged  = "REPLACED_BY_CHANGED"
	RuleStabilityPromoted  = "STABILITY_PROMOTED"
	RuleStabilityDemoted   = "STABILITY_DEMOTED"

	// Flags.

	RuleFlagNoDelete                 = "FLAG_NO_DELETE"
	RuleFlagReplaced                 = "FLAG_REPLACED"
	RuleFlagAdded                    = "FLAG_ADDED"
	RuleFlagRequiredAdded            = "FLAG_REQUIRED_ADDED"
	RuleFlagRequiredRemoved          = "FLAG_REQUIRED_REMOVED"
	RuleFlagNameChanged              = "FLAG_NAME_CHANGED"
	RuleFlagIdentifierNoDelete       = "FLAG_IDENTIFIER_NO_DELETE"
	RuleFlagIdentifierAdded          = "FLAG_IDENTIFIER_ADDED"
	RuleFlagHiddenIdentifierNoDelete = "FLAG_HIDDEN_IDENTIFIER_NO_DELETE"
	RuleFlagHiddenIdentifierAdded    = "FLAG_HIDDEN_IDENTIFIER_ADDED"
	RuleFlagIdentifierMoved          = "FLAG_IDENTIFIER_MOVED"
	RuleFlagNegatedNoDelete          = "FLAG_NEGATED_NO_DELETE"
	RuleFlagNegatedAdded             = "FLAG_NEGATED_ADDED"
	RuleFlagNoLongerCascades         = "FLAG_NO_LONGER_CASCADES"
	RuleFlagCascades                 = "FLAG_CASCADES"
	RuleFlagShortCircuitRemoved      = "FLAG_SHORT_CIRCUIT_REMOVED"
	RuleFlagShortCircuitAdded        = "FLAG_SHORT_CIRCUIT_ADDED"
	RuleFlagRepeatForbidden          = "FLAG_REPEAT_FORBIDDEN"
	RuleFlagRepeatAllowed            = "FLAG_REPEAT_ALLOWED"
	RuleFlagRoleChanged              = "FLAG_ROLE_CHANGED"

	// Arguments.

	RuleArgumentNoDelete           = "ARGUMENT_NO_DELETE"
	RuleArgumentAdded              = "ARGUMENT_ADDED"
	RuleArgumentRequiredAdded      = "ARGUMENT_REQUIRED_ADDED"
	RuleArgumentRequiredRemoved    = "ARGUMENT_REQUIRED_REMOVED"
	RuleArgumentNameChanged        = "ARGUMENT_NAME_CHANGED"
	RuleArgumentVariadicRemoved    = "ARGUMENT_VARIADIC_REMOVED"
	RuleArgumentVariadicAdded      = "ARGUMENT_VARIADIC_ADDED"
	RuleArgumentPassthroughChanged = "ARGUMENT_PASSTHROUGH_CHANGED"
	RuleArgumentGlobChanged        = "ARGUMENT_GLOB_CHANGED"

	// Environment variables.

	RuleEnvNoDelete         = "ENV_NO_DELETE"
	RuleEnvAdded            = "ENV_ADDED"
	RuleEnvRequiredAdded    = "ENV_REQUIRED_ADDED"
	RuleEnvRequiredRemoved  = "ENV_REQUIRED_REMOVED"
	RuleEnvVariableNoDelete = "ENV_VARIABLE_NO_DELETE"
	RuleEnvVariableAdded    = "ENV_VARIABLE_ADDED"
	RuleEnvNestingChanged   = "ENV_NESTING_CHANGED"

	// Config keys.

	RuleConfigNoDelete        = "CONFIG_NO_DELETE"
	RuleConfigAdded           = "CONFIG_ADDED"
	RuleConfigRequiredAdded   = "CONFIG_REQUIRED_ADDED"
	RuleConfigRequiredRemoved = "CONFIG_REQUIRED_REMOVED"
	RuleConfigMoved           = "CONFIG_MOVED"

	// Facts every input kind shares.

	RuleInputTypeChanged           = "INPUT_TYPE_CHANGED"
	RuleInputTypeWidened           = "INPUT_TYPE_WIDENED"
	RuleInputTypeNowDescribed      = "INPUT_TYPE_NOW_DESCRIBED"
	RuleInputKindChanged           = "INPUT_KIND_CHANGED"
	RuleInputSeparatorChanged      = "INPUT_SEPARATOR_CHANGED"
	RuleInputFromNoDelete          = "INPUT_FROM_NO_DELETE"
	RuleInputFromAdded             = "INPUT_FROM_ADDED"
	RuleInputImplicitValueAdded    = "INPUT_IMPLICIT_VALUE_ADDED"
	RuleInputImplicitValueRemoved  = "INPUT_IMPLICIT_VALUE_REMOVED"
	RuleInputImplicitValueChanged  = "INPUT_IMPLICIT_VALUE_CHANGED"
	RuleInputIgnoreCaseRemoved     = "INPUT_IGNORE_CASE_REMOVED"
	RuleInputIgnoreCaseAdded       = "INPUT_IGNORE_CASE_ADDED"
	RuleInputLayoutNoDelete        = "INPUT_LAYOUT_NO_DELETE"
	RuleInputLayoutAdded           = "INPUT_LAYOUT_ADDED"
	RuleInputLayoutFirstChanged    = "INPUT_LAYOUT_FIRST_CHANGED"
	RuleInputRelativeAdded         = "INPUT_RELATIVE_ADDED"
	RuleInputRelativeChanged       = "INPUT_RELATIVE_CHANGED"
	RuleInputExpandAdded           = "INPUT_EXPAND_ADDED"
	RuleInputExpandRemoved         = "INPUT_EXPAND_REMOVED"
	RuleInputRelativeToChanged     = "INPUT_RELATIVE_TO_CHANGED"
	RuleInputValuesFromAdded       = "INPUT_VALUES_FROM_ADDED"
	RuleInputValuesFromRemoved     = "INPUT_VALUES_FROM_REMOVED"
	RuleInputValuesFromChanged     = "INPUT_VALUES_FROM_CHANGED"
	RuleInputEnvNoDelete           = "INPUT_ENV_NO_DELETE"
	RuleInputEnvAdded              = "INPUT_ENV_ADDED"
	RuleInputVariableFileNoDelete  = "INPUT_VARIABLE_FILE_NO_DELETE"
	RuleInputVariableFileAdded     = "INPUT_VARIABLE_FILE_ADDED"
	RuleInputConfigKeyChanged      = "INPUT_CONFIG_KEY_CHANGED"
	RuleInputConfigKeyAdded        = "INPUT_CONFIG_KEY_ADDED"
	RuleInputConfigSourceChanged   = "INPUT_CONFIG_SOURCE_CHANGED"
	RuleInputDottedKeysChanged     = "INPUT_DOTTED_KEYS_CHANGED"
	RuleInputSecretChanged         = "INPUT_SECRET_CHANGED"
	RuleInputHidden                = "INPUT_HIDDEN"
	RuleInputUnhidden              = "INPUT_UNHIDDEN"
	RuleInputDefaultChanged        = "INPUT_DEFAULT_CHANGED"
	RuleInputDefaultRemoved        = "INPUT_DEFAULT_REMOVED"
	RuleInputDefaultAdded          = "INPUT_DEFAULT_ADDED"
	RuleInputEnumAdded             = "INPUT_ENUM_ADDED"
	RuleInputEnumRemoved           = "INPUT_ENUM_REMOVED"
	RuleEnumValueNoDelete          = "ENUM_VALUE_NO_DELETE"
	RuleEnumValueReplaced          = "ENUM_VALUE_REPLACED"
	RuleEnumValueAdded             = "ENUM_VALUE_ADDED"
	RuleEnumAliasNoDelete          = "ENUM_ALIAS_NO_DELETE"
	RuleEnumAliasAdded             = "ENUM_ALIAS_ADDED"
	RuleEnumValueHidden            = "ENUM_VALUE_HIDDEN"
	RuleEnumValueUnhidden          = "ENUM_VALUE_UNHIDDEN"
	RuleInputBoundAdded            = "INPUT_BOUND_ADDED"
	RuleInputBoundNarrowed         = "INPUT_BOUND_NARROWED"
	RuleInputBoundWidened          = "INPUT_BOUND_WIDENED"
	RuleInputBoundRemoved          = "INPUT_BOUND_REMOVED"
	RuleInputPatternAdded          = "INPUT_PATTERN_ADDED"
	RuleInputPatternChanged        = "INPUT_PATTERN_CHANGED"
	RuleInputPatternRemoved        = "INPUT_PATTERN_REMOVED"
	RuleInputFormatChanged         = "INPUT_FORMAT_CHANGED"
	RuleInputFormatRemoved         = "INPUT_FORMAT_REMOVED"
	RuleInputPropertyRequiredAdded = "INPUT_PROPERTY_REQUIRED_ADDED"
	RuleInputPropertyNoDelete      = "INPUT_PROPERTY_NO_DELETE"
	RuleInputPropertyAdded         = "INPUT_PROPERTY_ADDED"
	RuleInputClosed                = "INPUT_ADDITIONAL_PROPERTIES_REMOVED"
	RuleSchemaCompositionChanged   = "SCHEMA_COMPOSITION_CHANGED"

	// Output, a command's or an exit status's.

	RuleOutputTypeChanged            = "OUTPUT_TYPE_CHANGED"
	RuleOutputPropertyNoDelete       = "OUTPUT_PROPERTY_NO_DELETE"
	RuleOutputPropertyRequiredRemove = "OUTPUT_PROPERTY_REQUIRED_REMOVED"
	RuleOutputPropertyAdded          = "OUTPUT_PROPERTY_ADDED"
	RuleOutputEnumValueAdded         = "OUTPUT_ENUM_VALUE_ADDED"
	RuleOutputEnumValueRemoved       = "OUTPUT_ENUM_VALUE_REMOVED"
	RuleOutputEnumAdded              = "OUTPUT_ENUM_ADDED"
	RuleOutputEnumRemoved            = "OUTPUT_ENUM_REMOVED"

	// What a command or flag does, and what AI agents are offered.

	RuleEffectsAdded         = "EFFECTS_ADDED"
	RuleEffectsRemoved       = "EFFECTS_REMOVED"
	RuleEffectsRaised        = "EFFECTS_RAISED"
	RuleEffectsLowered       = "EFFECTS_LOWERED"
	RuleFlagRoleAdded        = "FLAG_ROLE_ADDED"
	RuleFlagRoleValueChanged = "FLAG_ROLE_VALUE_CHANGED"
	RuleAgentRemoved         = "AGENT_REMOVED"
	RuleAgentAdded           = "AGENT_ADDED"

	// A configuration file's profiles.

	RuleProfilesAdded          = "PROFILES_ADDED"
	RuleProfilesNoDelete       = "PROFILES_NO_DELETE"
	RuleProfilesUnderChanged   = "PROFILES_UNDER_CHANGED"
	RuleProfilesFlagNoDelete   = "PROFILES_FLAG_NO_DELETE"
	RuleProfilesFlagAdded      = "PROFILES_FLAG_ADDED"
	RuleProfilesEnvNoDelete    = "PROFILES_ENV_NO_DELETE"
	RuleProfilesEnvAdded       = "PROFILES_ENV_ADDED"
	RuleProfilesDefaultChanged = "PROFILES_DEFAULT_CHANGED"

	// Rules for contract fields added later go here, and in the catalog below.
)

// catalog lists every rule ID, in the order above.
var catalog = []string{
	RuleRootRenamed, RuleErrorsChanged, RuleMulticallNoDelete, RuleMulticallChanged, RuleMulticallAdded,
	RuleTopicRemoved, RuleTopicAdded, RuleCompletionEnvRemoved, RuleCompletionEnvChanged, RuleCompletionEnvAdded,
	RuleResponseFilesAdded, RuleResponseFilesPrefixChanged, RuleResponseFilesRemoved,

	RuleCommandNoDelete, RuleCommandReplaced, RuleCommandRenamedAliasKept, RuleCommandAdded, RuleCommandHidden,
	RuleCommandUnhidden, RuleAliasNoDelete, RuleAliasAdded, RuleHiddenAliasNoDelete, RuleHiddenAliasAdded,
	RuleAliasMoved, RulePluginNoDelete, RulePluginAdded, RuleOptionsFirstChanged, RuleCommandPassthroughChanged,
	RuleDigitFlagAdded, RuleFlagGroupAdded, RuleFlagGroupRemoved, RuleFlagGroupTightened, RuleFlagGroupLoosened,
	RuleFlagDependencyAdded, RuleFlagDependencyRemoved, RuleFlagDependencyTightened, RuleFlagDependencyLoosened,
	RuleConfigFileNoDelete, RuleConfigFileMoved, RuleConfigFileAsChanged, RuleConfigFileAdded,
	RulePluginDiscoveryNoDelete, RulePluginDiscoveryChanged, RulePluginDiscoveryAdded,
	RuleStdinAdded, RuleStdinNoDelete, RuleStdinFormatChanged, RuleStdinRequiredAdded, RuleStdinRequiredRemoved,
	RuleStdinTypeChanged, RuleStdinUnlessChanged, RuleStdinSeparatorChanged,
	RuleOutputNoDelete, RuleOutputAdded, RuleOutputStreamChanged,
	RuleExitStatusNoDelete, RuleExitStatusAdded, RuleExitSummaryChanged, RuleExitNameChanged, RuleExitNameAdded,
	RuleExitRetryableRemoved, RuleExitRetryableAdded,

	RuleDeprecationAdded, RuleDeprecationChanged, RuleDeprecationRemoved, RuleLifecycleChanged,
	RuleReplacedByChanged, RuleStabilityPromoted, RuleStabilityDemoted,

	RuleFlagNoDelete, RuleFlagReplaced, RuleFlagAdded, RuleFlagRequiredAdded, RuleFlagRequiredRemoved,
	RuleFlagNameChanged, RuleFlagIdentifierNoDelete, RuleFlagIdentifierAdded, RuleFlagHiddenIdentifierNoDelete,
	RuleFlagHiddenIdentifierAdded, RuleFlagIdentifierMoved, RuleFlagNegatedNoDelete, RuleFlagNegatedAdded,
	RuleFlagNoLongerCascades, RuleFlagCascades, RuleFlagShortCircuitRemoved, RuleFlagShortCircuitAdded,
	RuleFlagRepeatForbidden, RuleFlagRepeatAllowed, RuleFlagRoleChanged,

	RuleArgumentNoDelete, RuleArgumentAdded, RuleArgumentRequiredAdded, RuleArgumentRequiredRemoved,
	RuleArgumentNameChanged, RuleArgumentVariadicRemoved, RuleArgumentVariadicAdded,
	RuleArgumentPassthroughChanged, RuleArgumentGlobChanged,

	RuleEnvNoDelete, RuleEnvAdded, RuleEnvRequiredAdded, RuleEnvRequiredRemoved, RuleEnvVariableNoDelete,
	RuleEnvVariableAdded, RuleEnvNestingChanged,

	RuleConfigNoDelete, RuleConfigAdded, RuleConfigRequiredAdded, RuleConfigRequiredRemoved, RuleConfigMoved,

	RuleInputTypeChanged, RuleInputTypeWidened, RuleInputTypeNowDescribed, RuleInputKindChanged,
	RuleInputSeparatorChanged, RuleInputFromNoDelete, RuleInputFromAdded, RuleInputImplicitValueAdded,
	RuleInputImplicitValueRemoved, RuleInputImplicitValueChanged, RuleInputIgnoreCaseRemoved,
	RuleInputIgnoreCaseAdded, RuleInputLayoutNoDelete, RuleInputLayoutAdded, RuleInputLayoutFirstChanged,
	RuleInputRelativeAdded, RuleInputRelativeChanged, RuleInputExpandAdded, RuleInputExpandRemoved,
	RuleInputRelativeToChanged, RuleInputValuesFromAdded, RuleInputValuesFromRemoved, RuleInputValuesFromChanged,
	RuleInputEnvNoDelete, RuleInputEnvAdded, RuleInputVariableFileNoDelete, RuleInputVariableFileAdded,
	RuleInputConfigKeyChanged, RuleInputConfigKeyAdded, RuleInputConfigSourceChanged, RuleInputDottedKeysChanged,
	RuleInputSecretChanged, RuleInputHidden, RuleInputUnhidden, RuleInputDefaultChanged, RuleInputDefaultRemoved,
	RuleInputDefaultAdded, RuleInputEnumAdded, RuleInputEnumRemoved, RuleEnumValueNoDelete, RuleEnumValueReplaced,
	RuleEnumValueAdded, RuleEnumAliasNoDelete, RuleEnumAliasAdded, RuleEnumValueHidden, RuleEnumValueUnhidden,
	RuleInputBoundAdded, RuleInputBoundNarrowed, RuleInputBoundWidened, RuleInputBoundRemoved,
	RuleInputPatternAdded, RuleInputPatternChanged, RuleInputPatternRemoved, RuleInputFormatChanged,
	RuleInputFormatRemoved, RuleInputPropertyRequiredAdded, RuleInputPropertyNoDelete, RuleInputPropertyAdded,
	RuleInputClosed, RuleSchemaCompositionChanged,

	RuleOutputTypeChanged, RuleOutputPropertyNoDelete, RuleOutputPropertyRequiredRemove, RuleOutputPropertyAdded,
	RuleOutputEnumValueAdded, RuleOutputEnumValueRemoved, RuleOutputEnumAdded, RuleOutputEnumRemoved,

	RuleEffectsAdded, RuleEffectsRemoved, RuleEffectsRaised, RuleEffectsLowered, RuleFlagRoleAdded,
	RuleFlagRoleValueChanged, RuleAgentRemoved, RuleAgentAdded,

	RuleProfilesAdded, RuleProfilesNoDelete, RuleProfilesUnderChanged, RuleProfilesFlagNoDelete,
	RuleProfilesFlagAdded, RuleProfilesEnvNoDelete, RuleProfilesEnvAdded, RuleProfilesDefaultChanged,
}

// Rules lists every rule ID a finding can carry.
func Rules() []string { return slices.Clone(catalog) }

// KnownRule reports whether id is a rule ID.
func KnownRule(id string) bool { return slices.Contains(catalog, id) }
