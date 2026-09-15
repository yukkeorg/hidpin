// TinyUSB configuration for the hidpin firmware.
#ifndef HIDPIN_TUSB_CONFIG_H
#define HIDPIN_TUSB_CONFIG_H

#ifndef CFG_TUSB_MCU
#error CFG_TUSB_MCU must be defined
#endif

#ifndef BOARD_TUD_RHPORT
#define BOARD_TUD_RHPORT 0
#endif

#ifndef CFG_TUSB_OS
#define CFG_TUSB_OS OPT_OS_NONE
#endif

#define CFG_TUD_ENABLED 1
#define CFG_TUD_ENDPOINT0_SIZE 64

#define CFG_TUD_HID 1
#define CFG_TUD_HID_EP_BUFSIZE 64  // report ID + 63-byte payload

#if HIDPIN_DEBUG
#define CFG_TUD_CDC 1
#define CFG_TUD_CDC_RX_BUFSIZE 64
#define CFG_TUD_CDC_TX_BUFSIZE 256
#else
#define CFG_TUD_CDC 0
#endif

#define CFG_TUD_MSC 0
#define CFG_TUD_MIDI 0
#define CFG_TUD_VENDOR 0

#endif
