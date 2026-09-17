#include <string.h>

#include "pico/unique_id.h"
#include "tusb.h"

#include "hidpin/protocol.h"

// Set with -DHIDPIN_USB_VID / -DHIDPIN_USB_PID at configure time. The defaults are the
// pid.codes test PID, which is valid for in-house testing only, never for redistribution.
#ifndef HIDPIN_USB_VID
#define HIDPIN_USB_VID 0x1209
#endif
#ifndef HIDPIN_USB_PID
#define HIDPIN_USB_PID 0x0001
#endif
#define USB_BCD_DEVICE ((HIDPIN_FW_MAJOR << 8) | (HIDPIN_FW_MINOR << 4) | HIDPIN_FW_PATCH)

enum {
    ITF_NUM_HID = 0,
#if HIDPIN_DEBUG
    ITF_NUM_CDC,
    ITF_NUM_CDC_DATA,
#endif
    ITF_NUM_TOTAL
};

enum {
    STRID_LANGID = 0,
    STRID_MANUFACTURER,
    STRID_PRODUCT,
    STRID_SERIAL,
    STRID_CDC,
    STRID_COUNT
};

#define EPNUM_HID_OUT 0x01
#define EPNUM_HID_IN 0x81
#define EPNUM_CDC_NOTIF 0x82
#define EPNUM_CDC_OUT 0x03
#define EPNUM_CDC_IN 0x83

static const tusb_desc_device_t desc_device = {
    .bLength = sizeof(tusb_desc_device_t),
    .bDescriptorType = TUSB_DESC_DEVICE,
    .bcdUSB = 0x0200,
#if HIDPIN_DEBUG
    // Interface Association Descriptor for the CDC function.
    .bDeviceClass = TUSB_CLASS_MISC,
    .bDeviceSubClass = MISC_SUBCLASS_COMMON,
    .bDeviceProtocol = MISC_PROTOCOL_IAD,
#else
    .bDeviceClass = 0x00,
    .bDeviceSubClass = 0x00,
    .bDeviceProtocol = 0x00,
#endif
    .bMaxPacketSize0 = CFG_TUD_ENDPOINT0_SIZE,
    .idVendor = HIDPIN_USB_VID,
    .idProduct = HIDPIN_USB_PID,
    .bcdDevice = USB_BCD_DEVICE,
    .iManufacturer = STRID_MANUFACTURER,
    .iProduct = STRID_PRODUCT,
    .iSerialNumber = STRID_SERIAL,
    .bNumConfigurations = 1,
};

#if HIDPIN_DEBUG
#define CONFIG_TOTAL_LEN (TUD_CONFIG_DESC_LEN + TUD_HID_INOUT_DESC_LEN + TUD_CDC_DESC_LEN)
#else
#define CONFIG_TOTAL_LEN (TUD_CONFIG_DESC_LEN + TUD_HID_INOUT_DESC_LEN)
#endif

static const uint8_t desc_configuration[] = {
    TUD_CONFIG_DESCRIPTOR(1, ITF_NUM_TOTAL, 0, CONFIG_TOTAL_LEN, 0, 100),
    TUD_HID_INOUT_DESCRIPTOR(ITF_NUM_HID, 0, HID_ITF_PROTOCOL_NONE, HP_HID_REPORT_DESCRIPTOR_LEN, EPNUM_HID_OUT,
                             EPNUM_HID_IN, CFG_TUD_HID_EP_BUFSIZE, 1),
#if HIDPIN_DEBUG
    TUD_CDC_DESCRIPTOR(ITF_NUM_CDC, STRID_CDC, EPNUM_CDC_NOTIF, 8, EPNUM_CDC_OUT, EPNUM_CDC_IN, 64),
#endif
};

static const char *const string_desc[STRID_COUNT] = {
    [STRID_MANUFACTURER] = "yukke.org",
    [STRID_PRODUCT] = "hidpin",
    [STRID_CDC] = "hidpin debug",
};

uint8_t const *tud_descriptor_device_cb(void)
{
    return (uint8_t const *)&desc_device;
}

uint8_t const *tud_descriptor_configuration_cb(uint8_t index)
{
    (void)index;
    return desc_configuration;
}

uint8_t const *tud_hid_descriptor_report_cb(uint8_t instance)
{
    (void)instance;
    return hp_hid_report_descriptor;
}

uint16_t const *tud_descriptor_string_cb(uint8_t index, uint16_t langid)
{
    (void)langid;
    static uint16_t desc_str[33];
    char serial[2 * PICO_UNIQUE_BOARD_ID_SIZE_BYTES + 1];
    size_t chr_count;

    if (index == STRID_LANGID) {
        desc_str[1] = 0x0409;  // English (United States)
        chr_count = 1;
    } else {
        const char *str;
        if (index == STRID_SERIAL) {
            pico_get_unique_board_id_string(serial, sizeof(serial));
            str = serial;
        } else if (index < STRID_COUNT && string_desc[index] != NULL) {
            str = string_desc[index];
        } else {
            return NULL;
        }
        chr_count = strlen(str);
        if (chr_count > 32u) {
            chr_count = 32u;
        }
        for (size_t i = 0; i < chr_count; i++) {
            desc_str[1 + i] = (uint16_t)str[i];
        }
    }

    desc_str[0] = (uint16_t)((TUSB_DESC_STRING << 8) | (2u * chr_count + 2u));
    return desc_str;
}
