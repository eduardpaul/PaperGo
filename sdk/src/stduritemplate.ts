// The bundle replaces @std-uritemplate/std-uritemplate, which Kiota's
// RequestInformation.URL uses, with the expander for Kiota's own templates.
import { expand } from "./uritemplate.js";

export const StdUriTemplate = { expand };
