# rules

always do what is realistic for real BLE communication on ios and android.
everything starts with the package wire/ and all its tests.

From there we have kotlin/ and swift/ packages that use wire/ pacakge.

wire/ is just the ble radio layer, nothing specific to ios or android goes in there.

kotlin/ and swift/ are just go versions of the same functions the real devices will have.

the iphone/ and android/ packages use swift/ and kotlin/ to make simulated phones.

# imporant

only work in the wire/ directory right now. We are refactoring it to be more real
and are making breaking changes to other packages.
