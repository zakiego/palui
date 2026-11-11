// An agent's own Telegram bot: the Telegram runtime under the tgbot manifest.
import { createTelegramRuntime } from "../telegram/runtime"
import manifest from "./manifest"

export default createTelegramRuntime({}, manifest)
