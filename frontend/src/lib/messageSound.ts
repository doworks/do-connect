// The sound for a new message.
//
// The clinic turns it on for everyone and picks the usual sound, how loud it
// is and when it plays; each person can keep that or choose their own. With
// Do Connect open in several tabs, the tabs agree so a message sounds once,
// and nobody hears it for the chat they're reading.

export const MESSAGE_SOUNDS = ['chime', 'pop', 'bell', 'drop', 'classic'] as const
export type MessageSoundId = typeof MESSAGE_SOUNDS[number]
export type MessageSoundWhen = 'away' | 'always'

const FILES: Record<MessageSoundId, string> = {
  chime: '/sounds/chime.wav',
  pop: '/sounds/pop.wav',
  bell: '/sounds/bell.wav',
  drop: '/sounds/drop.wav',
  classic: '/notification.mp3',
}

export interface ClinicSound {
  message_sound_enabled: boolean
  message_sound: MessageSoundId
  message_sound_volume: number
  message_sound_when: MessageSoundWhen
}

/** A person's own choice; anything missing follows the clinic. */
export interface PersonalSound {
  message_sound?: '' | 'off' | MessageSoundId
  message_sound_volume?: number | null
  message_sound_when?: '' | MessageSoundWhen
  new_message_alerts?: boolean
}

export const CLINIC_DEFAULT: ClinicSound = {
  message_sound_enabled: true,
  message_sound: 'chime',
  message_sound_volume: 60,
  message_sound_when: 'always',
}

export interface ResolvedSound {
  sound: MessageSoundId
  volume: number
  when: MessageSoundWhen
}

/** What plays for this person, or null for silence. */
export function resolveSound(clinic: ClinicSound, mine: PersonalSound): ResolvedSound | null {
  if (!clinic.message_sound_enabled) return null
  const own = mine.message_sound
  // Before there was a sound setting, turning alerts off also silenced them.
  if (own === 'off' || (own === undefined && mine.new_message_alerts === false)) return null
  const sound = own && (MESSAGE_SOUNDS as readonly string[]).includes(own) ? own as MessageSoundId : clinic.message_sound
  const volume = typeof mine.message_sound_volume === 'number' ? mine.message_sound_volume : clinic.message_sound_volume
  if (volume <= 0) return null
  return { sound, volume: Math.min(100, volume), when: mine.message_sound_when || clinic.message_sound_when }
}

export function soundFile(sound: MessageSoundId): string {
  const base = ((window as any).__BASE_PATH__ ?? '').replace(/\/$/, '')
  return base + FILES[sound]
}

// ---------------------------------------------------------------- tabs

/** Someone is in Do Connect: a tab had focus within this long. */
const LOOKING_MS = 6000
const PRESENCE_KEY = 'doconnect.presence'
const HEARD_KEY = 'doconnect.heard'

export interface Presence {
  tab: string
  at: number
  contact: string | null
}

export interface MessageSituation {
  /** Someone has a Do Connect tab in front of them, and which chat it shows. */
  presence: Presence | null
  now: number
  contactId: string
}

/** Whether this message should make a sound, given where people are looking. */
export function shouldSound(resolved: ResolvedSound | null, s: MessageSituation): boolean {
  if (!resolved) return false
  const looking = !!s.presence && s.now - s.presence.at < LOOKING_MS
  if (!looking) return true
  if (resolved.when === 'away') return false
  return s.presence!.contact !== s.contactId
}

const tabId = Math.random().toString(36).slice(2)

function readJSON<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) as T : null
  } catch {
    return null
  }
}

function writeJSON(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Private windows can refuse storage; each tab then decides alone.
  }
}

export function readPresence(): Presence | null {
  return readJSON<Presence>(PRESENCE_KEY)
}

let presenceTimer: ReturnType<typeof setInterval> | null = null

/**
 * Keeps a note of whether this tab is in front of someone and which chat it
 * shows, so every tab can tell whether someone is looking.
 */
export function trackPresence(openContact: () => string | null) {
  if (presenceTimer || typeof document === 'undefined') return
  const beat = () => {
    const inFront = document.visibilityState === 'visible' && document.hasFocus()
    const current = readPresence()
    if (inFront) {
      writeJSON(PRESENCE_KEY, { tab: tabId, at: Date.now(), contact: openContact() })
    } else if (current?.tab === tabId && current.at) {
      writeJSON(PRESENCE_KEY, { ...current, at: 0 })
    }
  }
  window.addEventListener('focus', beat)
  window.addEventListener('blur', beat)
  document.addEventListener('visibilitychange', beat)
  presenceTimer = setInterval(beat, 2000)
  beat()
}

/** Call after switching chats, so other tabs know straight away. */
export function presenceChanged(openContact: string | null) {
  if (typeof document === 'undefined') return
  if (document.visibilityState === 'visible' && document.hasFocus()) {
    writeJSON(PRESENCE_KEY, { tab: tabId, at: Date.now(), contact: openContact })
  }
}

/**
 * True for the one tab that should play the sound for this message. A tab the
 * browser won't let play sound yet (never clicked) leaves it to the others.
 *
 * The browser's lock manager decides: storage reaches other tabs a moment
 * late, so two tabs could both read "not heard yet". The winner holds a lock
 * named after the message for a few seconds, and also notes it in storage
 * for any tab that hears about the message later.
 */
async function claim(key: string): Promise<boolean> {
  if (heardBy(key)) return false
  const locks = (navigator as any).locks
  if (locks?.request) {
    try {
      return await new Promise<boolean>((resolve, reject) => {
        locks.request(`doconnect.sound.${key}`, { ifAvailable: true }, async (lock: unknown) => {
          if (!lock || heardBy(key)) {
            resolve(false)
            return
          }
          markHeard(key)
          resolve(true)
          await new Promise(done => setTimeout(done, HOLD_MS))
        }).catch(reject)
      })
    } catch {
      // Fall through to deciding alone.
    }
  }
  if (heardBy(key)) return false
  markHeard(key)
  return true
}

const HOLD_MS = 5000

function markHeard(key: string) {
  const heard = readJSON<string[]>(HEARD_KEY) || []
  writeJSON(HEARD_KEY, [...heard.slice(-49), key])
}

function heardBy(key: string): boolean {
  return (readJSON<string[]>(HEARD_KEY) || []).includes(key)
}

function canPlay(): boolean {
  const activation = (navigator as any).userActivation
  return !activation || activation.hasBeenActive
}

// ---------------------------------------------------------------- playing

let player: HTMLAudioElement | null = null

export function playSound(resolved: ResolvedSound): Promise<boolean> {
  try {
    const src = soundFile(resolved.sound)
    if (!player || !player.src.endsWith(src)) player = new Audio(src)
    player.volume = Math.max(0, Math.min(1, resolved.volume / 100))
    player.currentTime = 0
    return player.play().then(() => true, () => false)
  } catch {
    return Promise.resolve(false)
  }
}

/** One pass of a sound at a volume, for the settings screens. */
export function previewSound(sound: MessageSoundId, volume: number) {
  const audio = new Audio(soundFile(sound))
  audio.volume = Math.max(0, Math.min(1, volume / 100))
  audio.play().catch(() => {})
}

let blockedNoticeShown = false

/**
 * Plays the message sound once across tabs. `onBlocked` is told when no tab
 * could play it because the browser waits for a first click.
 */
export async function soundForMessage(
  key: string,
  resolved: ResolvedSound | null,
  contactId: string,
  onBlocked?: () => void,
) {
  if (!shouldSound(resolved, { presence: readPresence(), now: Date.now(), contactId })) return
  if (canPlay()) {
    if (await claim(key)) {
      const played = await playSound(resolved!)
      if (!played) noteBlocked(onBlocked)
    }
    return
  }
  // Give a tab that may play a moment to take it.
  setTimeout(() => {
    if (!heardBy(key)) noteBlocked(onBlocked)
  }, 400)
}

function noteBlocked(onBlocked?: () => void) {
  if (blockedNoticeShown || !onBlocked) return
  blockedNoticeShown = true
  onBlocked()
}
